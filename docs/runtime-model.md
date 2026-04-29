# Forge Runtime Model

Forge separates platform dependencies from application dependencies.

## Current Model

Workers install a small baseline toolchain with Ansible:

- `git`, `make`, `gcc`, and `sqlite3` for Forge itself.
- `python3`, `python3-venv`, and `python3-pip` for Python demo/runtime support.
- Go tooling while Forge binaries are built directly on the host.

Application dependencies should be installed by the app's own `forge.yaml`
inside the deployment workdir. For Python, prefer a virtualenv:

```yaml
build:
  commands:
    - python3 -m venv .venv
    - . .venv/bin/activate && python -m pip install -r requirements.txt
run:
  command: . .venv/bin/activate && uvicorn app:app --host 0.0.0.0 --port $PORT
```

Do not add every application package to the Forge Ansible playbook. The playbook
should install runtime managers and safe baseline tools, not each app's
libraries.

## Ports

Apps should bind to `$PORT`. Forge allocates a unique host port per deployment
from `FORGE_APP_PORT_START..FORGE_APP_PORT_END` and injects it into the run
environment.

The `run.port` value in `forge.yaml` is kept as a manifest/default port, but the
platform may assign a different host port to avoid collisions with other apps.

Default range:

```text
20000-39999
```

## Recommended Evolution

For real multi-language use, Forge should move toward one of these models:

1. Runtime profiles: worker groups labeled `python`, `node`, `go`, `java`, etc.,
   each with its own baseline packages.
2. Buildpacks: detect app language and build an isolated app directory with a
   standardized launch command.
3. Containers: build or pull OCI images and run each app in its own network,
   process, and filesystem namespace.

Containers are the clean long-term answer because app dependencies, system
packages, ports, and processes are isolated per deployment. Until then, use
per-app environments such as Python virtualenvs, Node `node_modules`, Go module
build outputs, or Java build artifacts inside the deployment workdir.
