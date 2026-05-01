# Forge Runtime Model

Forge separates platform dependencies from application dependencies.

## Current Model

Workers install a small baseline toolchain with Ansible:

- `git`, `make`, and `gcc` for Forge itself. The control plane embeds a pure-Go SQLite driver; `sqlite3` is installed by Ansible only as an operational/debugging CLI.
- `python3`, `python3-venv`, and `python3-pip` for Python demo/runtime support.
- Go tooling while Forge binaries are built directly on the host.

Application dependencies should be installed by the app's own `forge.yaml` inside the deployment workdir. For Python, prefer a virtualenv:

```yaml
build:
  commands:
    - python3 -m venv .venv
    - . .venv/bin/activate && python -m pip install -r requirements.txt
run:
  command: . .venv/bin/activate && uvicorn app:app --host 0.0.0.0 --port $PORT
```

`forge.yaml` is parsed with `gopkg.in/yaml.v3` and unknown fields are rejected, so configuration mistakes fail before any build command runs.

Do not add every application package to the Forge Ansible playbook. The playbook should install runtime managers and safe baseline tools, not each app's libraries.

The worker service runs as the unprivileged `forge` user. Build and run commands still execute shell defined by the application repository, so only repositories listed in `FORGE_ALLOWED_REPOS` should be allowed to trigger deployments.
The systemd unit also denies access to common instance metadata addresses, so application commands should not be able to read cloud metadata from the worker.
Production workers set `FORGE_REQUIRE_ISOLATION=true`, which makes build tasks fail closed if the build runner cannot create Linux namespaces.

## Ports

Apps should bind to `$PORT`. Forge allocates a unique host port per deployment from `FORGE_APP_PORT_START..FORGE_APP_PORT_END` and injects it into the run environment.

The `run.port` value in `forge.yaml` is kept as a manifest/default port, but the platform may assign a different host port to avoid collisions with other apps.

Default range:

```text
20000-39999
```

## Recommended Evolution

For real multi-language use, Forge should probably move toward one of these models:

1. Runtime profiles: worker groups labeled `python`, `node`, `go`, `java`, etc., each with its own baseline packages.
2. Buildpacks: detect app language and build an isolated app directory with a standardized launch command.
3. Containers: build or pull OCI images and run each app in its own network, process, and filesystem namespace.

Containers are the clean long-term answer because app dependencies, system packages, ports, and processes are isolated per deployment. Until then, use per-app environments such as Python virtualenvs, Node `node_modules`, Go module build outputs, or Java build artifacts inside the deployment workdir.
