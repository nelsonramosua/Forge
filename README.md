# Forge - Self-Hosted Deployment Platform

Forge is a mini Render/Fly.io-style deployment platform designed for self-hosting: a Go control plane, a C11 worker agent, a native Linux build runner, Caddy for dynamic routing, and Prometheus/Grafana/Alertmanager for observability.

## System Map

- **Control plane:** Go REST API, GitHub webhook validation, SSE event stream, scheduler, SQLite WAL state, encrypted secret vault, and Caddy Admin API integration.
- **forge-agent:** C11 worker process that polls for tasks, streams logs to the control plane, reports `/proc` metrics, launches app processes, and exposes Prometheus metrics through a Unix socket.
- **Build runner:** C11 Linux runner that applies cgroups v2 and attempts PID, mount, and network namespaces without Docker.
- **Reverse proxy:** Caddy dynamic JSON config via the Admin API on `:2019`.
- **Observability:** Prometheus scrape configs, Alertmanager alerts, Grafana dashboard, and a small Unix-socket exporter.
- **Infrastructure:** Terraform stacks for AWS Free Tier-style deployments and OCI Always Free experiments, plus Ansible bootstrapping for the control plane and workers.

## Repository Layout

- `control-plane/cmd/forge-control-plane`: control plane entrypoint.
- `control-plane/internal`: control plane packages for config, server, SQLite store, Caddy, vault, and `forge.yaml` parsing.
- `agent/src`: C11 worker agent.
- `build-runner/src`: C11 build runner.
- `cmd/forge-exporter`: Prometheus exporter that proxies the agent's Unix socket metrics.
- `infra`: AWS/OCI Terraform and Ansible automation.
- `observability`: Prometheus, Alertmanager, and Grafana assets.
- `docs/runbooks`: SRE runbooks and SLOs.

## forge.yaml

Each app repository needs a `forge.yaml` at the repository root:

```yaml
name: myapp
runtime: python3.11
build:
  commands:
    - python3 -m venv .venv
    - . .venv/bin/activate && python -m pip install -r requirements.txt
run:
  command: . .venv/bin/activate && uvicorn app:main --host 0.0.0.0 --port $PORT
  port: 8000
resources:
  memory: 256M
  cpu: 0.5
health:
  path: /health
  interval: 10s
  timeout: 3s
  retries: 3
env:
  - DATABASE_URL
  - SECRET_KEY
```

## Build

```sh
make build
make test
```

The control plane intentionally uses the system `sqlite3` executable so the project builds with the Go standard library in restricted environments. The Ansible playbook installs `sqlite3` on hosts.

## Run Locally

Terminal 1:

```sh
FORGE_ADDR=:8080 \
FORGE_DB_PATH=data/forge.db \
FORGE_WORK_DIR=data/work \
FORGE_BASE_DOMAIN=forge.localhost \
make run-control-plane
```

Terminal 2:

```sh
FORGE_CONTROL_PLANE_URL=http://127.0.0.1:8080 \
FORGE_RUNNER_PATH=./bin/forge-build-runner \
FORGE_METRICS_SOCKET=/tmp/forge-agent-metrics.sock \
make run-agent
```

Terminal 3, optional exporter:

```sh
FORGE_AGENT_METRICS_SOCKET=/tmp/forge-agent-metrics.sock ./bin/forge-exporter
```

## API Surface

- `POST /api/v1/webhook/github`: validates `X-Hub-Signature-256`, clones the repo, parses `forge.yaml`, and creates a pending deployment.
- `GET /api/v1/events`: SSE stream for deployment and log events.
- `GET /api/v1/deployments`: lists recent deployments.
- `PUT /api/v1/apps/{app}/secrets/{key}`: encrypts and stores a secret value.
- `GET /metrics`: Prometheus metrics for the control plane.
- Agent endpoints under `/api/v1/agents` and `/api/v1/tasks` support registration, heartbeats, polling, log events, and task completion.

Set `FORGE_AGENT_TOKEN` to require bearer auth for agents and `FORGE_ADMIN_TOKEN` to require bearer auth for admin APIs.

## Deployment Workflow

1. GitHub sends a push webhook.
2. The control plane validates HMAC-SHA256, shallow-clones the repo, and parses `forge.yaml`.
3. The scheduler chooses the online worker with the most CPU and memory headroom.
4. The agent claims a build task, clones the app repo, and invokes `forge-build-runner` for each build command.
5. The build runner applies cgroup limits and attempts Linux namespace isolation before executing the command.
6. The agent claims the run task, starts the app with injected vault secrets and `PORT`, and performs health checks.
7. The control plane updates Caddy with `app.FORGE_BASE_DOMAIN -> worker:PORT` and marks the deployment `running`.
8. Events and logs stream through SSE; metrics are scraped by Prometheus.

## Infrastructure

```sh
cd infra/terraform/aws
terraform init
terraform apply
ansible-playbook -i ../../ansible/inventory.ini ../../ansible/playbook.yml
```

Terraform can create either the AWS two-node topology or the OCI topology. Ansible compiles Forge on the target hosts, renders Ansible Vault-backed env files, and installs Caddy, Prometheus, Alertmanager, Grafana, and worker services.

See [docs/aws-deploy.md](docs/aws-deploy.md) for the AWS fallback path,
[docs/oci-deploy.md](docs/oci-deploy.md) for the OCI, DNS, and secrets flow,
and [docs/runtime-model.md](docs/runtime-model.md) for app dependency and port
isolation guidance.

## Testing

```sh
scripts/check-local.sh
scripts/e2e-local.sh
scripts/check-iac.sh
```

See [docs/testing.md](docs/testing.md) for local, IaC, OCI smoke, and failure-case tests.

## Design Decisions

| Decision | Choice | Rejected Alternative | Reason |
| :--- | :--- | :--- | :--- |
| Control plane language | Go | Python/FastAPI | Native concurrency, single deployment binary, and simple long-poll/SSE handling. |
| Agent language | C11 | Go/Rust | Direct access to Linux process, cgroup, namespace, socket, and `/proc` interfaces. |
| Config store | SQLite WAL | etcd/PostgreSQL | No external database dependency while preserving durable platform state. |
| Proxy | Caddy | nginx | Native JSON Admin API and automatic TLS support. |
| Isolation | cgroups v2 + namespaces | Docker/containerd | No daemon dependency; the runner exercises the Linux primitives directly. |
| Infra provider | AWS and OCI Terraform stacks | Single-provider lock-in | AWS is the current practical path; OCI remains available for future Always Free capacity. |
