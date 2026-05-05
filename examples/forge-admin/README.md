# Forge Admin Console

This directory contains the bundled Forge admin console example. It is a small Python stdlib HTTP server plus static assets that proxy the control-plane admin API behind a login session.

## What it does

- Serves a login screen and a dashboard for workers, apps, and recent deployments.
- Proxies authenticated requests to the control plane using `FORGE_ADMIN_TOKEN`.
- Uses `FORGE_ADMIN_CONSOLE_PASSWORD` for browser login and stores a session cookie in the browser.
- Exposes `/health` for Forge health checks.

## Deploying it

1. Publish this directory as its own GitHub repository.
2. Set `FORGE_ADMIN_APP_REPO` or `forge_admin_app_repo` to that repository.
3. Deploy an app whose `forge.yaml` uses `name: admin`.

Required environment variables:

- `FORGE_ADMIN_TOKEN`
- `FORGE_ADMIN_CONSOLE_PASSWORD`
- `FORGE_CONTROL_PLANE_PRIVATE_IP` (preferred), or `FORGE_CONTROL_PLANE_URL`

When `FORGE_CONTROL_PLANE_PRIVATE_IP` is set, the console proxies the control plane at `http://FORGE_CONTROL_PLANE_PRIVATE_IP:8080`. Set `FORGE_CONTROL_PLANE_URL` only when you need to override that derived URL.

Optional environment variables:

- `FORGE_ADMIN_SESSION_TTL_SECONDS`

The app intentionally has no third-party Python dependencies.
