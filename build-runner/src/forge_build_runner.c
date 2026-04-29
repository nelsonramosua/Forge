#define _GNU_SOURCE

#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#define STACK_SIZE (1024 * 1024)

struct runner_config {
    const char *workdir;
    const char *cgroup;
    const char *memory_bytes;
    const char *cpu_quota;
    char **command;
};

struct child_config {
    const struct runner_config *cfg;
};

static void usage(const char *program) {
    fprintf(stderr,
            "usage: %s --workdir DIR --cgroup NAME [--memory-bytes BYTES] [--cpu-quota QUOTA] -- COMMAND [ARGS...]\n",
            program);
}

static int write_file(const char *path, const char *value) {
    int fd = open(path, O_WRONLY | O_CLOEXEC);
    if (fd < 0) {
        return -1;
    }
    size_t len = strlen(value);
    ssize_t written = write(fd, value, len);
    close(fd);
    return written == (ssize_t)len ? 0 : -1;
}

static int mkdir_p(const char *path) {
    char tmp[512];
    size_t len = strlen(path);
    if (len >= sizeof(tmp)) {
        errno = ENAMETOOLONG;
        return -1;
    }
    strcpy(tmp, path);
    for (char *p = tmp + 1; *p; p++) {
        if (*p == '/') {
            *p = '\0';
            if (mkdir(tmp, 0755) < 0 && errno != EEXIST) {
                return -1;
            }
            *p = '/';
        }
    }
    if (mkdir(tmp, 0755) < 0 && errno != EEXIST) {
        return -1;
    }
    return 0;
}

static int configure_cgroup(const struct runner_config *cfg, char *path, size_t path_len) {
    if (!cfg->cgroup || cfg->cgroup[0] == '\0') {
        return -1;
    }
    snprintf(path, path_len, "/sys/fs/cgroup/forge/%s", cfg->cgroup);
    if (mkdir_p(path) < 0) {
        fprintf(stderr, "forge-build-runner: cgroup setup skipped: %s\n", strerror(errno));
        return -1;
    }

    char file[640];
    if (cfg->memory_bytes && cfg->memory_bytes[0] != '\0') {
        snprintf(file, sizeof(file), "%s/memory.max", path);
        if (write_file(file, cfg->memory_bytes) < 0) {
            fprintf(stderr, "forge-build-runner: memory.max not applied: %s\n", strerror(errno));
        }
    }
    if (cfg->cpu_quota && cfg->cpu_quota[0] != '\0') {
        char cpu_max[128];
        snprintf(cpu_max, sizeof(cpu_max), "%s 100000", cfg->cpu_quota);
        snprintf(file, sizeof(file), "%s/cpu.max", path);
        if (write_file(file, cpu_max) < 0) {
            fprintf(stderr, "forge-build-runner: cpu.max not applied: %s\n", strerror(errno));
        }
    }
    return 0;
}

static void attach_pid_to_cgroup(const char *cgroup_path, pid_t pid) {
    if (!cgroup_path || cgroup_path[0] == '\0') {
        return;
    }
    char file[640];
    char value[64];
    snprintf(file, sizeof(file), "%s/cgroup.procs", cgroup_path);
    snprintf(value, sizeof(value), "%ld", (long)pid);
    if (write_file(file, value) < 0) {
        fprintf(stderr, "forge-build-runner: cgroup attach skipped: %s\n", strerror(errno));
    }
}

static int child_main(void *arg) {
    struct child_config *child = (struct child_config *)arg;
    const struct runner_config *cfg = child->cfg;

    if (chdir(cfg->workdir) < 0) {
        fprintf(stderr, "forge-build-runner: chdir(%s): %s\n", cfg->workdir, strerror(errno));
        _exit(127);
    }
    if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) < 0 && errno != EPERM) {
        fprintf(stderr, "forge-build-runner: mount propagation setup failed: %s\n", strerror(errno));
    }
    execvp(cfg->command[0], cfg->command);
    fprintf(stderr, "forge-build-runner: execvp(%s): %s\n", cfg->command[0], strerror(errno));
    _exit(127);
}

static int wait_for_child(pid_t pid) {
    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) {
            continue;
        }
        perror("forge-build-runner: waitpid");
        return 127;
    }
    if (WIFEXITED(status)) {
        return WEXITSTATUS(status);
    }
    if (WIFSIGNALED(status)) {
        return 128 + WTERMSIG(status);
    }
    return 127;
}

static int run_with_fork(const struct runner_config *cfg, const char *cgroup_path) {
    pid_t pid = fork();
    if (pid < 0) {
        perror("forge-build-runner: fork");
        return 127;
    }
    if (pid == 0) {
        struct child_config child = {.cfg = cfg};
        child_main(&child);
    }
    attach_pid_to_cgroup(cgroup_path, pid);
    return wait_for_child(pid);
}

static int run_isolated(const struct runner_config *cfg, const char *cgroup_path) {
    void *stack = malloc(STACK_SIZE);
    if (!stack) {
        return run_with_fork(cfg, cgroup_path);
    }
    struct child_config child = {.cfg = cfg};
    int flags = CLONE_NEWPID | CLONE_NEWNS | SIGCHLD;
    pid_t pid = clone(child_main, (char *)stack + STACK_SIZE, flags, &child);
    if (pid < 0) {
        if (errno == EPERM || errno == EINVAL) {
            fprintf(stderr, "forge-build-runner: namespaces unavailable, falling back to fork: %s\n", strerror(errno));
            free(stack);
            return run_with_fork(cfg, cgroup_path);
        }
        perror("forge-build-runner: clone");
        free(stack);
        return 127;
    }
    attach_pid_to_cgroup(cgroup_path, pid);
    int code = wait_for_child(pid);
    free(stack);
    return code;
}

static bool parse_args(int argc, char **argv, struct runner_config *cfg) {
    memset(cfg, 0, sizeof(*cfg));
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--") == 0) {
            if (i + 1 >= argc) {
                return false;
            }
            cfg->command = &argv[i + 1];
            break;
        }
        if (i + 1 >= argc) {
            return false;
        }
        if (strcmp(argv[i], "--workdir") == 0) {
            cfg->workdir = argv[++i];
        } else if (strcmp(argv[i], "--cgroup") == 0) {
            cfg->cgroup = argv[++i];
        } else if (strcmp(argv[i], "--memory-bytes") == 0) {
            cfg->memory_bytes = argv[++i];
        } else if (strcmp(argv[i], "--cpu-quota") == 0) {
            cfg->cpu_quota = argv[++i];
        } else {
            return false;
        }
    }
    return cfg->workdir && cfg->cgroup && cfg->command && cfg->command[0];
}

int main(int argc, char **argv) {
    struct runner_config cfg;
    if (!parse_args(argc, argv, &cfg)) {
        usage(argv[0]);
        return 2;
    }

    char cgroup_path[512] = {0};
    configure_cgroup(&cfg, cgroup_path, sizeof(cgroup_path));
    return run_isolated(&cfg, cgroup_path);
}
