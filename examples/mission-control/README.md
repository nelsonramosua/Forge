# Forge Mission Control

A richer Forge demo app that looks like an operations dashboard instead of a bare status page.

It shows:

- rollout milestones and recent deploy events,
- runtime metadata from Forge environment variables,
- a health endpoint and a small JSON API.

## Run Locally

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
APP_MESSAGE="Launch approved" APP_ENV=staging APP_VERSION=v2.1.0 uvicorn app:app --host 0.0.0.0 --port 8000
```

Open:

```text
http://127.0.0.1:8000/
```

## Deploy With Forge

Create a public repository with these files, then point your webhook at:

```text
https://YOUR_BASE_DOMAIN/api/v1/webhook/github
```

The app will deploy at:

```text
https://mission-control.YOUR_BASE_DOMAIN/
```