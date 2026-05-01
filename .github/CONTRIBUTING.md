# Contributing to Forge

Thanks for taking the time. This document covers how to set up a development environment, the conventions the codebase follows, and the process for getting a change merged.

---

## Table of contents

- [Prerequisites](#prerequisites)
- [Building from source](#building-from-source)
- [Running the test suite](#running-the-test-suite)
- [End-to-end local test](#end-to-end-local-test)
- [Code conventions](#code-conventions)
- [Submitting a change](#submitting-a-change)
- [Reporting a security issue](#reporting-a-security-issue)

---

## Prerequisites

| Tool | Minimum version | Required for |
|---|---|---|
| Go | 1.21 | Control plane, exporter |
| GCC | 11 | forge-agent, forge-build-runner |
| Make | any | Build orchestration |
| Python 3 | 3.9 | e2e test script (uses stdlib only) |
| OpenSSL | any | Generating test credentials |
| Terraform | 1.6 | Infra changes (optional) |
| Ansible | 2.14 | Infra changes (optional) |

Linux is required to build and run the agent and build runner — both use Linux-specific APIs (`cgroups v2`, `clone(2)` with namespace flags, `/proc`). The control plane and exporter build and run on macOS, but the full stack only works on Linux.

---

## Building from source

```sh
make build
```

This produces four binaries under `bin/`:

| Binary | Language | Description |
|---|---|---|
| `forge-control-plane` | Go | API server + scheduler |
| `forge-exporter` | Go | Prometheus exporter bridge |
| `forge-agent` | C11 | Worker agent |
| `forge-build-runner` | C11 | Isolated build executor |

To build individual targets:

```sh
make bin/forge-control-plane
make bin/forge-agent
```

---

## Running the test suite

```sh
make test                  # go test ./...
go test -race ./...        # with race detector (recommended before opening a PR!)
go vet ./...               # static analysis
scripts/check-local.sh     # build + test + vet + race + smoke-runs bin/forge-build-runner
scripts/check-iac.sh       # terraform fmt/validate + ansible syntax-check (requires optional tools)
```

There are no C unit tests (yet). 
Correctness for the agent and build runner is covered by the end-to-end test below.

---

## End-to-end local test

`scripts/e2e-local.sh` starts a real control plane and agent in the background, creates a minimal Python app in a temp git repo, fires a signed webhook, and waits for the deployment to reach `status=running` and respond to a health check.

```sh
scripts/e2e-local.sh
```

The script requires cgroups v2 to be available and mounted at `/sys/fs/cgroup`. It will fail on macOS or inside most CI environments that don't grant cgroup access (which is why the CI workflow skips it and runs only the unit tests and build checks).

---

## Code conventions

### Go

- Standard `gofmt` / `goimports` formatting. Run before committing.
- Error handling: wrap with `fmt.Errorf("context: %w", err)` — do not use bare `errors.New` for errors that cross package boundaries.
- All DB queries use prepared statements via `modernc.org/sqlite`. Direct string interpolation into SQL is not allowed.
- Secrets must never appear in log output. Use `slog` structured logging throughout.
- Tokens compared with `crypto/subtle.ConstantTimeCompare`.

### C

- C11 standard (`-std=c11 -Wall -Wextra -Werror`). All warnings are errors.
- All buffers have explicit length parameters. Functions that write to a caller-supplied buffer must return `false` / a negative error code if the value would be truncated — silent truncation is not allowed.
- Error paths must close all open file descriptors and free all heap allocations before returning.
- `fork()` followed by `exec()` is the only concurrency model in the agent. No dynamic libraries other than libc and libpthread are allowed.
- The JSON parser (`json_parser.c/h`) is the only permitted way to parse JSON in the agent. Do not add ad-hoc string scanning.

### Infra

- Terraform: run `terraform fmt` before committing. All new variables need a `description` and a `type`. 
  Default values must be safe for a fresh account (no hardcoded IPs, no open 0.0.0.0/0 ingress).
- Ansible: all secrets go through `ansible-vault`. Plaintext credentials must **never** appear in playbooks, templates, or committed inventory files.
- `forge.yaml` schema changes must be backward compatible unless there is a compelling reason.
  New fields must be optional with documented defaults.

---

## Submitting a change

1. **Open an issue first** for anything non-trivial. For bug fixes with a clear root cause, a PR without a prior issue is fine.

2. **Fork and branch.** Branch names don't need to follow a specific format, but `fix/`, `feat/`, `docs/`, `refactor/` prefixes help.

3. **Keep commits focused.** One logical change per commit. Commit messages should explain *why*, not just *what*.

4. **Fill in the PR template.** Pay particular attention to the `forge.yaml` compatibility section and the testing checklist.

5. **CI must pass.** The workflow runs `make build`, `go test -race ./...`, `go vet ./...`, and a `gcc -Werror` compile of both C binaries. A red CI is a blocker.

6. **One approval required** to merge. For security-sensitive changes (auth, secret handling, cgroup/namespace code), expect more scrutiny.

---

## Reporting a security issue

Do not open a public issue for vulnerabilities that are exploitable against a running Forge instance. Use GitHub's [private vulnerability reporting](../../security/advisories/new) instead.

For lower-severity findings (hardening gaps, information disclosure, dependency advisories), the [security issue template](../../issues/new?template=security.md) is appropriate.