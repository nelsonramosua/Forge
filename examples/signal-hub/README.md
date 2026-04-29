# Forge Signal Hub

A Go example that shows Forge with a non-Python app.

It builds to a single binary and serves:

- `/healthz` for health checks,
- `/api/snapshot` for machine-readable deployment metadata,
- `/` for a compact operations dashboard.

## Run Locally

```sh
go build -o signal-hub .
PORT=8000 HUB_TITLE="Signal Hub" HUB_REGION="local" ./signal-hub
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
https://signal-hub.YOUR_BASE_DOMAIN/
```