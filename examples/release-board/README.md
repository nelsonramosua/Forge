# Forge Release Board

A small Forge demo app for end-to-end deployment testing.

It exposes:

- `/health` for health checks.
- `/` for a simple release status page.
- `/api/status` for machine-readable deployment status.

## Run Locally

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
APP_MESSAGE="Deployed by Forge" uvicorn app:app --host 0.0.0.0 --port 8000
```

Open:

```text
http://127.0.0.1:8000/
```

## Deploy With Forge

Create a public GitHub repository containing these files, then configure a
GitHub push webhook pointing to Forge:

```text
https://YOUR_BASE_DOMAIN/api/v1/webhook/github
```

The deployed app will be available at:

```text
https://release-board.YOUR_BASE_DOMAIN/
```
