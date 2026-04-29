#define _POSIX_C_SOURCE 200809L

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <pthread.h>
#include <signal.h>
#include <stdbool.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/types.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#define MAX_COMMANDS 32
#define MAX_ENV 64
#define READ_CHUNK 4096

struct agent_config {
    char control_url[256];
    char agent_id[128];
    char token[256];
    char runner_path[256];
    char metrics_socket[256];
    char advertised_address[128];
    int poll_sleep_seconds;
};

struct http_response {
    int status;
    char *body;
};

struct env_pair {
    char *key;
    char *value;
};

struct task {
    long id;
    long deployment_id;
    char type[32];
    char app_name[128];
    char repo_url[512];
    char commit_sha[128];
    char workdir[512];
    char run_command[1024];
    char health_path[256];
    char health_interval[32];
    char health_timeout[32];
    int health_retries;
    int port;
    long memory_bytes;
    double cpu;
    char *build_commands[MAX_COMMANDS];
    int build_command_count;
    struct env_pair env[MAX_ENV];
    int env_count;
};

struct metrics_state {
    pthread_mutex_t mu;
    double cpu_used;
    long memory_used;
    long memory_capacity;
    int running_processes;
    time_t last_heartbeat;
};

struct log_thread_arg {
    struct agent_config cfg;
    long task_id;
    int fd;
};

static volatile sig_atomic_t running = 1;
static struct metrics_state metrics = {.mu = PTHREAD_MUTEX_INITIALIZER};

static void on_signal(int signo) {
    (void)signo;
    running = 0;
}

static const char *env_or(const char *key, const char *fallback) {
    const char *value = getenv(key);
    return value && value[0] ? value : fallback;
}

static void load_config(struct agent_config *cfg) {
    memset(cfg, 0, sizeof(*cfg));
    snprintf(cfg->control_url, sizeof(cfg->control_url), "%s", env_or("FORGE_CONTROL_PLANE_URL", "http://127.0.0.1:8080"));
    snprintf(cfg->token, sizeof(cfg->token), "%s", env_or("FORGE_AGENT_TOKEN", ""));
    snprintf(cfg->runner_path, sizeof(cfg->runner_path), "%s", env_or("FORGE_RUNNER_PATH", "./bin/forge-build-runner"));
    snprintf(cfg->metrics_socket, sizeof(cfg->metrics_socket), "%s", env_or("FORGE_METRICS_SOCKET", "/tmp/forge-agent-metrics.sock"));
    snprintf(cfg->advertised_address, sizeof(cfg->advertised_address), "%s", env_or("FORGE_AGENT_ADDRESS", ""));
    cfg->poll_sleep_seconds = atoi(env_or("FORGE_AGENT_POLL_SECONDS", "2"));
    if (cfg->poll_sleep_seconds <= 0) {
        cfg->poll_sleep_seconds = 2;
    }
    const char *agent_id = getenv("FORGE_AGENT_ID");
    if (agent_id && agent_id[0]) {
        snprintf(cfg->agent_id, sizeof(cfg->agent_id), "%s", agent_id);
    } else if (gethostname(cfg->agent_id, sizeof(cfg->agent_id) - 1) < 0) {
        snprintf(cfg->agent_id, sizeof(cfg->agent_id), "forge-agent");
    }
}

static int mkdir_p(const char *path) {
    char tmp[768];
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

static char *json_escape(const char *input) {
    size_t len = strlen(input);
    char *out = calloc(1, len * 2 + 1);
    if (!out) {
        return NULL;
    }
    char *p = out;
    for (const unsigned char *s = (const unsigned char *)input; *s; s++) {
        switch (*s) {
            case '\\':
            case '"':
                *p++ = '\\';
                *p++ = (char)*s;
                break;
            case '\n':
                *p++ = '\\';
                *p++ = 'n';
                break;
            case '\r':
                *p++ = '\\';
                *p++ = 'r';
                break;
            case '\t':
                *p++ = '\\';
                *p++ = 't';
                break;
            default:
                if (*s >= 32) {
                    *p++ = (char)*s;
                }
        }
    }
    return out;
}

static char *format_json(const char *fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    int needed = vsnprintf(NULL, 0, fmt, ap);
    va_end(ap);
    if (needed < 0) {
        return NULL;
    }
    char *out = calloc(1, (size_t)needed + 1);
    if (!out) {
        return NULL;
    }
    va_start(ap, fmt);
    vsnprintf(out, (size_t)needed + 1, fmt, ap);
    va_end(ap);
    return out;
}

static bool parse_control_url(const char *url, char *host, size_t host_len, int *port) {
    const char *p = url;
    if (strncmp(p, "http://", 7) == 0) {
        p += 7;
    } else if (strncmp(p, "https://", 8) == 0) {
        fprintf(stderr, "forge-agent: https control plane URLs require a TLS-capable proxy; use http:// locally\n");
        return false;
    }
    const char *slash = strchr(p, '/');
    size_t authority_len = slash ? (size_t)(slash - p) : strlen(p);
    const char *colon = memchr(p, ':', authority_len);
    if (colon) {
        size_t len = (size_t)(colon - p);
        if (len >= host_len) {
            return false;
        }
        memcpy(host, p, len);
        host[len] = '\0';
        *port = atoi(colon + 1);
    } else {
        if (authority_len >= host_len) {
            return false;
        }
        memcpy(host, p, authority_len);
        host[authority_len] = '\0';
        *port = 80;
    }
    return host[0] && *port > 0;
}

static int connect_tcp(const char *host, int port) {
    char port_text[16];
    snprintf(port_text, sizeof(port_text), "%d", port);
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_family = AF_UNSPEC;
    struct addrinfo *result = NULL;
    if (getaddrinfo(host, port_text, &hints, &result) != 0) {
        return -1;
    }
    int fd = -1;
    for (struct addrinfo *rp = result; rp; rp = rp->ai_next) {
        fd = socket(rp->ai_family, rp->ai_socktype, rp->ai_protocol);
        if (fd < 0) {
            continue;
        }
        if (connect(fd, rp->ai_addr, rp->ai_addrlen) == 0) {
            break;
        }
        close(fd);
        fd = -1;
    }
    freeaddrinfo(result);
    return fd;
}

static int append_buffer(char **buffer, size_t *len, size_t *cap, const char *data, size_t data_len) {
    if (*len + data_len + 1 > *cap) {
        size_t next = *cap ? *cap * 2 : 8192;
        while (*len + data_len + 1 > next) {
            next *= 2;
        }
        char *resized = realloc(*buffer, next);
        if (!resized) {
            return -1;
        }
        *buffer = resized;
        *cap = next;
    }
    memcpy(*buffer + *len, data, data_len);
    *len += data_len;
    (*buffer)[*len] = '\0';
    return 0;
}

static int http_request(const struct agent_config *cfg, const char *method, const char *path, const char *body, struct http_response *response) {
    memset(response, 0, sizeof(*response));
    char host[256];
    int port = 0;
    if (!parse_control_url(cfg->control_url, host, sizeof(host), &port)) {
        return -1;
    }
    int fd = connect_tcp(host, port);
    if (fd < 0) {
        return -1;
    }
    size_t body_len = body ? strlen(body) : 0;
    char header[2048];
    int header_len = snprintf(header, sizeof(header),
                              "%s %s HTTP/1.1\r\n"
                              "Host: %s\r\n"
                              "User-Agent: forge-agent/0.1\r\n"
                              "Connection: close\r\n"
                              "Content-Type: application/json\r\n"
                              "Content-Length: %zu\r\n"
                              "%s%s%s"
                              "\r\n",
                              method,
                              path,
                              host,
                              body_len,
                              cfg->token[0] ? "Authorization: Bearer " : "",
                              cfg->token[0] ? cfg->token : "",
                              cfg->token[0] ? "\r\n" : "");
    if (header_len < 0 || header_len >= (int)sizeof(header)) {
        close(fd);
        return -1;
    }
    if (write(fd, header, (size_t)header_len) != header_len) {
        close(fd);
        return -1;
    }
    if (body_len && write(fd, body, body_len) != (ssize_t)body_len) {
        close(fd);
        return -1;
    }

    char *raw = NULL;
    size_t len = 0;
    size_t cap = 0;
    char chunk[READ_CHUNK];
    for (;;) {
        ssize_t n = read(fd, chunk, sizeof(chunk));
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            free(raw);
            close(fd);
            return -1;
        }
        if (n == 0) {
            break;
        }
        if (append_buffer(&raw, &len, &cap, chunk, (size_t)n) < 0) {
            free(raw);
            close(fd);
            return -1;
        }
    }
    close(fd);
    if (!raw) {
        raw = calloc(1, 1);
    }

    int status = 0;
    sscanf(raw, "HTTP/%*s %d", &status);
    char *body_start = strstr(raw, "\r\n\r\n");
    if (body_start) {
        body_start += 4;
        response->body = strdup(body_start);
    } else {
        response->body = strdup("");
    }
    response->status = status;
    free(raw);
    return 0;
}

static void http_response_free(struct http_response *response) {
    free(response->body);
    response->body = NULL;
}

static const char *find_json_value(const char *json, const char *key) {
    char pattern[128];
    snprintf(pattern, sizeof(pattern), "\"%s\"", key);
    const char *p = json;
    while ((p = strstr(p, pattern)) != NULL) {
        p += strlen(pattern);
        while (*p && isspace((unsigned char)*p)) {
            p++;
        }
        if (*p++ != ':') {
            continue;
        }
        while (*p && isspace((unsigned char)*p)) {
            p++;
        }
        return p;
    }
    return NULL;
}

static int hex_value(char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

static bool append_utf8(char **out, unsigned int codepoint) {
    if (codepoint <= 0x7F) {
        *(*out)++ = (char)codepoint;
        return true;
    }
    if (codepoint <= 0x7FF) {
        *(*out)++ = (char)(0xC0 | (codepoint >> 6));
        *(*out)++ = (char)(0x80 | (codepoint & 0x3F));
        return true;
    }
    if (codepoint >= 0xD800 && codepoint <= 0xDFFF) {
        return false;
    }
    if (codepoint <= 0xFFFF) {
        *(*out)++ = (char)(0xE0 | (codepoint >> 12));
        *(*out)++ = (char)(0x80 | ((codepoint >> 6) & 0x3F));
        *(*out)++ = (char)(0x80 | (codepoint & 0x3F));
        return true;
    }
    if (codepoint <= 0x10FFFF) {
        *(*out)++ = (char)(0xF0 | (codepoint >> 18));
        *(*out)++ = (char)(0x80 | ((codepoint >> 12) & 0x3F));
        *(*out)++ = (char)(0x80 | ((codepoint >> 6) & 0x3F));
        *(*out)++ = (char)(0x80 | (codepoint & 0x3F));
        return true;
    }
    return false;
}

static bool parse_unicode_escape(const char **cursor, unsigned int *codepoint) {
    unsigned int cp = 0;
    for (int i = 0; i < 4; i++) {
        int value = hex_value((*cursor)[i]);
        if (value < 0) {
            return false;
        }
        cp = (cp << 4) | (unsigned int)value;
    }
    *cursor += 4;
    *codepoint = cp;
    return true;
}

static bool json_decode_escape(const char **cursor, char **out) {
    char esc = *(*cursor)++;
    switch (esc) {
    case '"':
    case '\\':
    case '/':
        *(*out)++ = esc;
        return true;
    case 'b':
        *(*out)++ = '\b';
        return true;
    case 'f':
        *(*out)++ = '\f';
        return true;
    case 'n':
        *(*out)++ = '\n';
        return true;
    case 'r':
        *(*out)++ = '\r';
        return true;
    case 't':
        *(*out)++ = '\t';
        return true;
    case 'u': {
        unsigned int cp = 0;
        if (!parse_unicode_escape(cursor, &cp)) {
            return false;
        }
        return append_utf8(out, cp);
    }
    default:
        return false;
    }
}

static bool json_decode_escape_char(const char **cursor, unsigned char *out) {
    char buffer[5] = {0};
    char *write = buffer;
    if (!json_decode_escape(cursor, &write)) {
        return false;
    }
    if ((size_t)(write - buffer) != 1) {
        return false;
    }
    *out = (unsigned char)buffer[0];
    return true;
}

static bool json_get_string(const char *json, const char *key, char *out, size_t out_len) {
    const char *p = find_json_value(json, key);
    if (!p || *p != '"') {
        return false;
    }
    p++;
    size_t i = 0;
    while (*p && *p != '"') {
        unsigned char c = (unsigned char)*p++;
        if (c == '\\' && *p) {
            if (!json_decode_escape_char(&p, &c)) {
                return false;
            }
        }
        if (i + 1 < out_len) {
            out[i++] = (char)c;
        }
    }
    out[i] = '\0';
    return true;
}

static long json_get_long_default(const char *json, const char *key, long fallback) {
    const char *p = find_json_value(json, key);
    if (!p) {
        return fallback;
    }
    return strtol(p, NULL, 10);
}

static double json_get_double_default(const char *json, const char *key, double fallback) {
    const char *p = find_json_value(json, key);
    if (!p) {
        return fallback;
    }
    return strtod(p, NULL);
}

static char *parse_json_string_value(const char **cursor) {
    const char *p = *cursor;
    if (*p != '"') {
        return NULL;
    }
    p++;
    char *out = calloc(1, strlen(p) + 1);
    if (!out) {
        return NULL;
    }
    char *w = out;
    while (*p && *p != '"') {
        unsigned char c = (unsigned char)*p++;
        if (c == '\\' && *p) {
            if (!json_decode_escape(&p, &w)) {
                free(out);
                return NULL;
            }
            continue;
        }
        *w++ = (char)c;
    }
    if (*p == '"') {
        p++;
    }
    *cursor = p;
    return out;
}

static int json_get_string_array(const char *json, const char *key, char **items, int max_items) {
    const char *p = find_json_value(json, key);
    if (!p || *p != '[') {
        return 0;
    }
    p++;
    int count = 0;
    while (*p && *p != ']' && count < max_items) {
        while (*p && (isspace((unsigned char)*p) || *p == ',')) {
            p++;
        }
        if (*p == '"') {
            items[count++] = parse_json_string_value(&p);
        } else {
            break;
        }
    }
    return count;
}

static int json_get_env_object(const char *json, struct env_pair *env, int max_env) {
    const char *p = find_json_value(json, "env");
    if (!p || *p != '{') {
        return 0;
    }
    p++;
    int count = 0;
    while (*p && *p != '}' && count < max_env) {
        while (*p && (isspace((unsigned char)*p) || *p == ',')) {
            p++;
        }
        if (*p != '"') {
            break;
        }
        char *key = parse_json_string_value(&p);
        while (*p && isspace((unsigned char)*p)) {
            p++;
        }
        if (*p++ != ':') {
            free(key);
            break;
        }
        while (*p && isspace((unsigned char)*p)) {
            p++;
        }
        char *value = parse_json_string_value(&p);
        if (!key || !value) {
            free(key);
            free(value);
            break;
        }
        env[count].key = key;
        env[count].value = value;
        count++;
    }
    return count;
}

static void task_free(struct task *task) {
    for (int i = 0; i < task->build_command_count; i++) {
        free(task->build_commands[i]);
    }
    for (int i = 0; i < task->env_count; i++) {
        free(task->env[i].key);
        free(task->env[i].value);
    }
}

static bool parse_task(const char *json, struct task *task) {
    memset(task, 0, sizeof(*task));
    task->id = json_get_long_default(json, "id", 0);
    task->deployment_id = json_get_long_default(json, "deployment_id", 0);
    task->port = (int)json_get_long_default(json, "port", 0);
    task->memory_bytes = json_get_long_default(json, "memory_bytes", 0);
    task->cpu = json_get_double_default(json, "cpu", 0.0);
    task->health_retries = (int)json_get_long_default(json, "retries", 3);
    json_get_string(json, "type", task->type, sizeof(task->type));
    json_get_string(json, "app_name", task->app_name, sizeof(task->app_name));
    json_get_string(json, "repo_url", task->repo_url, sizeof(task->repo_url));
    json_get_string(json, "commit_sha", task->commit_sha, sizeof(task->commit_sha));
    json_get_string(json, "workdir", task->workdir, sizeof(task->workdir));
    json_get_string(json, "run_command", task->run_command, sizeof(task->run_command));
    json_get_string(json, "path", task->health_path, sizeof(task->health_path));
    json_get_string(json, "interval", task->health_interval, sizeof(task->health_interval));
    json_get_string(json, "timeout", task->health_timeout, sizeof(task->health_timeout));
    if (!task->health_path[0]) {
        snprintf(task->health_path, sizeof(task->health_path), "/");
    }
    if (!task->health_interval[0]) {
        snprintf(task->health_interval, sizeof(task->health_interval), "10s");
    }
    if (!task->health_timeout[0]) {
        snprintf(task->health_timeout, sizeof(task->health_timeout), "3s");
    }
    task->build_command_count = json_get_string_array(json, "build_commands", task->build_commands, MAX_COMMANDS);
    task->env_count = json_get_env_object(json, task->env, MAX_ENV);
    return task->id > 0 && task->deployment_id > 0 && task->type[0] && task->workdir[0];
}

static long read_meminfo_kb(const char *key) {
    FILE *fp = fopen("/proc/meminfo", "r");
    if (!fp) {
        return 0;
    }
    char line[256];
    long value = 0;
    while (fgets(line, sizeof(line), fp)) {
        if (strncmp(line, key, strlen(key)) == 0) {
            sscanf(line + strlen(key), ": %ld", &value);
            break;
        }
    }
    fclose(fp);
    return value;
}

static double read_load_average(void) {
    FILE *fp = fopen("/proc/loadavg", "r");
    if (!fp) {
        return 0.0;
    }
    double load = 0.0;
    if (fscanf(fp, "%lf", &load) != 1) {
        load = 0.0;
    }
    fclose(fp);
    return load;
}

static void refresh_metrics(void) {
    long total = read_meminfo_kb("MemTotal") * 1024;
    long available = read_meminfo_kb("MemAvailable") * 1024;
    pthread_mutex_lock(&metrics.mu);
    metrics.cpu_used = read_load_average();
    metrics.memory_capacity = total;
    metrics.memory_used = total > available ? total - available : 0;
    metrics.last_heartbeat = time(NULL);
    pthread_mutex_unlock(&metrics.mu);
}

static double cpu_capacity(void) {
    long cpus = sysconf(_SC_NPROCESSORS_ONLN);
    return cpus > 0 ? (double)cpus : 1.0;
}

static long memory_capacity(void) {
    long total = read_meminfo_kb("MemTotal") * 1024;
    return total > 0 ? total : 0;
}

static int post_event(const struct agent_config *cfg, long task_id, const char *level, const char *message) {
    char *escaped_level = json_escape(level);
    char *escaped_message = json_escape(message);
    char *body = format_json("{\"level\":\"%s\",\"message\":\"%s\"}", escaped_level ? escaped_level : "info", escaped_message ? escaped_message : "");
    free(escaped_level);
    free(escaped_message);
    if (!body) {
        return -1;
    }
    char path[128];
    snprintf(path, sizeof(path), "/api/v1/tasks/%ld/events", task_id);
    struct http_response resp;
    int rc = http_request(cfg, "POST", path, body, &resp);
    free(body);
    if (rc == 0) {
        http_response_free(&resp);
    }
    return rc;
}

static int complete_task(const struct agent_config *cfg, long task_id, const char *status, const char *message, pid_t pid, int port) {
    char *escaped_status = json_escape(status);
    char *escaped_message = json_escape(message);
    char *body = format_json("{\"status\":\"%s\",\"message\":\"%s\",\"pid\":%ld,\"port\":%d}",
                             escaped_status ? escaped_status : status,
                             escaped_message ? escaped_message : "",
                             (long)pid,
                             port);
    free(escaped_status);
    free(escaped_message);
    if (!body) {
        return -1;
    }
    char path[128];
    snprintf(path, sizeof(path), "/api/v1/tasks/%ld/complete", task_id);
    struct http_response resp;
    int rc = http_request(cfg, "POST", path, body, &resp);
    free(body);
    if (rc == 0) {
        http_response_free(&resp);
    }
    return rc;
}

static int run_capture(const struct agent_config *cfg, long task_id, char *const argv[], const char *cwd) {
    int pipefd[2];
    if (pipe(pipefd) < 0) {
        return 127;
    }
    pid_t pid = fork();
    if (pid < 0) {
        close(pipefd[0]);
        close(pipefd[1]);
        return 127;
    }
    if (pid == 0) {
        close(pipefd[0]);
        dup2(pipefd[1], STDOUT_FILENO);
        dup2(pipefd[1], STDERR_FILENO);
        close(pipefd[1]);
        if (cwd && cwd[0]) {
            if (chdir(cwd) < 0) {
                fprintf(stderr, "chdir(%s): %s\n", cwd, strerror(errno));
                _exit(127);
            }
        }
        execvp(argv[0], argv);
        fprintf(stderr, "execvp(%s): %s\n", argv[0], strerror(errno));
        _exit(127);
    }
    close(pipefd[1]);
    char buffer[READ_CHUNK + 1];
    while (true) {
        ssize_t n = read(pipefd[0], buffer, READ_CHUNK);
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            break;
        }
        if (n == 0) {
            break;
        }
        buffer[n] = '\0';
        fputs(buffer, stdout);
        post_event(cfg, task_id, "info", buffer);
    }
    close(pipefd[0]);
    int status = 0;
    while (waitpid(pid, &status, 0) < 0 && errno == EINTR) {
    }
    if (WIFEXITED(status)) {
        return WEXITSTATUS(status);
    }
    if (WIFSIGNALED(status)) {
        return 128 + WTERMSIG(status);
    }
    return 127;
}

static bool file_exists(const char *path) {
    struct stat st;
    return stat(path, &st) == 0;
}

static void join_path(char *out, size_t out_len, const char *a, const char *b) {
    snprintf(out, out_len, "%s/%s", a, b);
}

static int ensure_repo(const struct agent_config *cfg, const struct task *task, char *src, size_t src_len) {
    if (mkdir_p(task->workdir) < 0) {
        post_event(cfg, task->id, "error", strerror(errno));
        return 1;
    }
    join_path(src, src_len, task->workdir, "src");
    if (!file_exists(src)) {
        char *clone_argv[] = {"git", "clone", "--depth=1", (char *)task->repo_url, src, NULL};
        int code = run_capture(cfg, task->id, clone_argv, NULL);
        if (code != 0) {
            return code;
        }
    }
    if (task->commit_sha[0]) {
        char *checkout_argv[] = {"git", "-C", src, "checkout", (char *)task->commit_sha, NULL};
        int code = run_capture(cfg, task->id, checkout_argv, NULL);
        if (code != 0) {
            return code;
        }
    }
    return 0;
}

static int run_build_task(const struct agent_config *cfg, const struct task *task) {
    char src[768];
    int code = ensure_repo(cfg, task, src, sizeof(src));
    if (code != 0) {
        complete_task(cfg, task->id, "failed", "repository checkout failed", 0, 0);
        return code;
    }

    char memory[64];
    char cpu_quota[64];
    snprintf(memory, sizeof(memory), "%ld", task->memory_bytes);
    long quota = (long)(task->cpu * 100000.0);
    if (quota <= 0) {
        quota = 100000;
    }
    snprintf(cpu_quota, sizeof(cpu_quota), "%ld", quota);

    for (int i = 0; i < task->build_command_count; i++) {
        char cgroup[128];
        snprintf(cgroup, sizeof(cgroup), "build-%ld-%d", task->id, i);
        post_event(cfg, task->id, "info", task->build_commands[i]);
        char *argv[] = {
            (char *)cfg->runner_path,
            "--workdir",
            src,
            "--cgroup",
            cgroup,
            "--memory-bytes",
            memory,
            "--cpu-quota",
            cpu_quota,
            "--",
            "/bin/sh",
            "-lc",
            task->build_commands[i],
            NULL,
        };
        code = run_capture(cfg, task->id, argv, NULL);
        if (code != 0) {
            complete_task(cfg, task->id, "failed", "build command failed", 0, 0);
            return code;
        }
    }
    complete_task(cfg, task->id, "succeeded", "build completed", 0, 0);
    return 0;
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

static void attach_process_cgroup(const struct task *task, pid_t pid) {
    char cgroup_path[512];
    snprintf(cgroup_path, sizeof(cgroup_path), "/sys/fs/cgroup/forge/run-%ld", task->deployment_id);
    if (mkdir_p(cgroup_path) < 0) {
        return;
    }
    char file[640];
    char value[128];
    snprintf(file, sizeof(file), "%s/memory.max", cgroup_path);
    snprintf(value, sizeof(value), "%ld", task->memory_bytes);
    write_file(file, value);
    snprintf(file, sizeof(file), "%s/cpu.max", cgroup_path);
    long quota = (long)(task->cpu * 100000.0);
    if (quota <= 0) {
        quota = 100000;
    }
    snprintf(value, sizeof(value), "%ld 100000", quota);
    write_file(file, value);
    snprintf(file, sizeof(file), "%s/cgroup.procs", cgroup_path);
    snprintf(value, sizeof(value), "%ld", (long)pid);
    write_file(file, value);
}

static void *log_stream_thread(void *arg) {
    struct log_thread_arg *thread_arg = (struct log_thread_arg *)arg;
    char buffer[READ_CHUNK + 1];
    while (running) {
        ssize_t n = read(thread_arg->fd, buffer, READ_CHUNK);
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            break;
        }
        if (n == 0) {
            break;
        }
        buffer[n] = '\0';
        post_event(&thread_arg->cfg, thread_arg->task_id, "info", buffer);
    }
    close(thread_arg->fd);
    free(thread_arg);
    return NULL;
}

static int duration_seconds(const char *value, int fallback) {
    if (!value || !value[0]) {
        return fallback;
    }
    char *end = NULL;
    long n = strtol(value, &end, 10);
    if (n <= 0) {
        return fallback;
    }
    if (end && strcmp(end, "ms") == 0) {
        return (int)((n + 999) / 1000);
    }
    return (int)n;
}

static bool health_check_once(int port, const char *path) {
    int fd = connect_tcp("127.0.0.1", port);
    if (fd < 0) {
        return false;
    }
    char req[512];
    snprintf(req, sizeof(req), "GET %s HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n", path);
    if (write(fd, req, strlen(req)) < 0) {
        close(fd);
        return false;
    }
    char response[128] = {0};
    if (read(fd, response, sizeof(response) - 1) < 0) {
        close(fd);
        return false;
    }
    close(fd);
    int status = 0;
    sscanf(response, "HTTP/%*s %d", &status);
    return status >= 200 && status < 400;
}

static int run_app_task(const struct agent_config *cfg, const struct task *task) {
    char src[768];
    int code = ensure_repo(cfg, task, src, sizeof(src));
    if (code != 0) {
        complete_task(cfg, task->id, "failed", "repository checkout failed", 0, task->port);
        return code;
    }

    int pipefd[2];
    if (pipe(pipefd) < 0) {
        complete_task(cfg, task->id, "failed", "log pipe failed", 0, task->port);
        return 127;
    }
    pid_t pid = fork();
    if (pid < 0) {
        close(pipefd[0]);
        close(pipefd[1]);
        complete_task(cfg, task->id, "failed", "fork failed", 0, task->port);
        return 127;
    }
    if (pid == 0) {
        close(pipefd[0]);
        dup2(pipefd[1], STDOUT_FILENO);
        dup2(pipefd[1], STDERR_FILENO);
        close(pipefd[1]);
        setsid();
        if (chdir(src) < 0) {
            fprintf(stderr, "chdir(%s): %s\n", src, strerror(errno));
            _exit(127);
        }
        char port_text[32];
        snprintf(port_text, sizeof(port_text), "%d", task->port);
        setenv("PORT", port_text, 1);
        for (int i = 0; i < task->env_count; i++) {
            setenv(task->env[i].key, task->env[i].value, 1);
        }
        execl("/bin/sh", "sh", "-lc", task->run_command, (char *)NULL);
        fprintf(stderr, "exec run command: %s\n", strerror(errno));
        _exit(127);
    }
    close(pipefd[1]);
    attach_process_cgroup(task, pid);

    struct log_thread_arg *thread_arg = calloc(1, sizeof(*thread_arg));
    if (thread_arg) {
        thread_arg->cfg = *cfg;
        thread_arg->task_id = task->id;
        thread_arg->fd = pipefd[0];
        pthread_t thread;
        if (pthread_create(&thread, NULL, log_stream_thread, thread_arg) == 0) {
            pthread_detach(thread);
        } else {
            close(pipefd[0]);
            free(thread_arg);
        }
    }

    pthread_mutex_lock(&metrics.mu);
    metrics.running_processes++;
    pthread_mutex_unlock(&metrics.mu);

    int interval = duration_seconds(task->health_interval, 10);
    int retries = task->health_retries > 0 ? task->health_retries : 3;
    for (int i = 0; i < retries; i++) {
        sleep((unsigned int)interval);
        if (health_check_once(task->port, task->health_path)) {
            complete_task(cfg, task->id, "succeeded", "health checks passed", pid, task->port);
            return 0;
        }
    }
    kill(pid, SIGTERM);
    complete_task(cfg, task->id, "failed", "health checks failed", pid, task->port);
    return 1;
}

static int register_agent(const struct agent_config *cfg) {
    char hostname[128] = {0};
    gethostname(hostname, sizeof(hostname) - 1);
    refresh_metrics();
    long mem = memory_capacity();
    double cpus = cpu_capacity();
    char *id = json_escape(cfg->agent_id);
    char *host = json_escape(hostname);
    char *address = json_escape(cfg->advertised_address);
    char *body = format_json("{\"id\":\"%s\",\"hostname\":\"%s\",\"address\":\"%s\",\"cpu_capacity\":%.2f,\"memory_capacity\":%ld,\"cpu_used\":0,\"memory_used\":0}",
                             id ? id : cfg->agent_id,
                             host ? host : hostname,
                             address ? address : "",
                             cpus,
                             mem);
    free(id);
    free(host);
    free(address);
    if (!body) {
        return -1;
    }
    struct http_response resp;
    int rc = http_request(cfg, "POST", "/api/v1/agents/register", body, &resp);
    free(body);
    if (rc == 0) {
        rc = resp.status >= 200 && resp.status < 300 ? 0 : -1;
        http_response_free(&resp);
    }
    return rc;
}

static int heartbeat(const struct agent_config *cfg) {
    refresh_metrics();
    pthread_mutex_lock(&metrics.mu);
    double cpu_used = metrics.cpu_used;
    long memory_used = metrics.memory_used;
    pthread_mutex_unlock(&metrics.mu);
    char *address = json_escape(cfg->advertised_address);
    char *body = format_json("{\"address\":\"%s\",\"cpu_used\":%.2f,\"memory_used\":%ld}",
                             address ? address : "",
                             cpu_used,
                             memory_used);
    free(address);
    if (!body) {
        return -1;
    }
    char path[256];
    snprintf(path, sizeof(path), "/api/v1/agents/%s/heartbeat", cfg->agent_id);
    struct http_response resp;
    int rc = http_request(cfg, "POST", path, body, &resp);
    free(body);
    if (rc == 0) {
        http_response_free(&resp);
    }
    return rc;
}

static void handle_task(const struct agent_config *cfg, const char *json) {
    struct task task;
    if (!parse_task(json, &task)) {
        fprintf(stderr, "forge-agent: could not parse task: %s\n", json);
        return;
    }
    if (strcmp(task.type, "build") == 0) {
        run_build_task(cfg, &task);
    } else if (strcmp(task.type, "run") == 0) {
        run_app_task(cfg, &task);
    } else {
        complete_task(cfg, task.id, "failed", "unknown task type", 0, 0);
    }
    task_free(&task);
}

static void *metrics_server_thread(void *arg) {
    struct agent_config *cfg = (struct agent_config *)arg;
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) {
        perror("forge-agent metrics socket");
        return NULL;
    }
    unlink(cfg->metrics_socket);
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    if (strlen(cfg->metrics_socket) >= sizeof(addr.sun_path)) {
        fprintf(stderr, "forge-agent metrics socket path is too long: %s\n", cfg->metrics_socket);
        close(fd);
        return NULL;
    }
    strncpy(addr.sun_path, cfg->metrics_socket, sizeof(addr.sun_path) - 1);
    if (bind(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror("forge-agent metrics bind");
        close(fd);
        return NULL;
    }
    if (chmod(cfg->metrics_socket, 0660) < 0) {
        perror("forge-agent metrics chmod");
        close(fd);
        unlink(cfg->metrics_socket);
        return NULL;
    }
    if (listen(fd, 16) < 0) {
        perror("forge-agent metrics listen");
        close(fd);
        return NULL;
    }
    while (running) {
        int client = accept(fd, NULL, NULL);
        if (client < 0) {
            if (errno == EINTR) {
                continue;
            }
            break;
        }
        struct timeval timeout = {.tv_sec = 1, .tv_usec = 0};
        setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout));
        char request[1024];
        size_t request_len = 0;
        while (request_len < sizeof(request)) {
            ssize_t n = read(client, request + request_len, sizeof(request) - request_len);
            if (n > 0) {
                request_len += (size_t)n;
                if (request_len >= 4) {
                    for (size_t i = 0; i + 3 < request_len; i++) {
                        if (request[i] == '\r' && request[i + 1] == '\n' && request[i + 2] == '\r' && request[i + 3] == '\n') {
                            request_len = sizeof(request);
                            break;
                        }
                    }
                }
                continue;
            }
            if (n < 0 && errno == EINTR) {
                continue;
            }
            break;
        }
        pthread_mutex_lock(&metrics.mu);
        double cpu_used = metrics.cpu_used;
        long memory_used = metrics.memory_used;
        long memory_cap = metrics.memory_capacity;
        int processes = metrics.running_processes;
        time_t last = metrics.last_heartbeat;
        pthread_mutex_unlock(&metrics.mu);

        char body[1024];
        int body_len = snprintf(body, sizeof(body),
                                "# HELP forge_agent_cpu_used Load average reported by the agent.\n"
                                "# TYPE forge_agent_cpu_used gauge\n"
                                "forge_agent_cpu_used %.2f\n"
                                "# HELP forge_agent_memory_used_bytes Memory used on the worker.\n"
                                "# TYPE forge_agent_memory_used_bytes gauge\n"
                                "forge_agent_memory_used_bytes %ld\n"
                                "# HELP forge_agent_memory_capacity_bytes Memory capacity on the worker.\n"
                                "# TYPE forge_agent_memory_capacity_bytes gauge\n"
                                "forge_agent_memory_capacity_bytes %ld\n"
                                "# HELP forge_agent_processes Running app processes launched by the agent.\n"
                                "# TYPE forge_agent_processes gauge\n"
                                "forge_agent_processes %d\n"
                                "# HELP forge_agent_last_heartbeat_seconds Last heartbeat unix timestamp.\n"
                                "# TYPE forge_agent_last_heartbeat_seconds gauge\n"
                                "forge_agent_last_heartbeat_seconds %ld\n",
                                cpu_used,
                                memory_used,
                                memory_cap,
                                processes,
                                (long)last);
        char header[256];
        int header_len = snprintf(header, sizeof(header),
                                  "HTTP/1.1 200 OK\r\nContent-Type: text/plain; version=0.0.4\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
                                  body_len);
        if (write(client, header, (size_t)header_len) < 0) {
            close(client);
            continue;
        }
        if (write(client, body, (size_t)body_len) < 0) {
            close(client);
            continue;
        }
        close(client);
    }
    close(fd);
    unlink(cfg->metrics_socket);
    return NULL;
}

int main(void) {
    signal(SIGINT, on_signal);
    signal(SIGTERM, on_signal);
    signal(SIGPIPE, SIG_IGN);

    struct agent_config cfg;
    load_config(&cfg);
    refresh_metrics();

    pthread_t metrics_thread;
    struct agent_config *metrics_cfg = calloc(1, sizeof(*metrics_cfg));
    if (metrics_cfg) {
        *metrics_cfg = cfg;
        if (pthread_create(&metrics_thread, NULL, metrics_server_thread, metrics_cfg) == 0) {
            pthread_detach(metrics_thread);
        }
    }

    while (running && register_agent(&cfg) != 0) {
        fprintf(stderr, "forge-agent: registration failed; retrying\n");
        sleep(2);
    }

    while (running) {
        heartbeat(&cfg);
        char path[256];
        snprintf(path, sizeof(path), "/api/v1/agents/%s/tasks", cfg.agent_id);
        struct http_response resp;
        if (http_request(&cfg, "GET", path, NULL, &resp) == 0) {
            if (resp.status == 200 && resp.body && resp.body[0]) {
                handle_task(&cfg, resp.body);
            }
            http_response_free(&resp);
        }
        sleep((unsigned int)cfg.poll_sleep_seconds);
    }
    return 0;
}
