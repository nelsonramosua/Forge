# Forge Admin Console

This directory contains the bundled Forge admin console example. It is a small Python stdlib HTTP server plus static assets that proxy the control-plane admin API behind a login session.

## What it does

- Serves a login screen and a dashboard for workers, apps, and recent deployments.
- Triggers manual deploys, redeploys branch HEAD, retries failed/stopped deployments, rolls back to previous commits, and stops/cancels deployments.
- Manages app secret keys and GitHub repo credentials without displaying stored values.
- Proxies authenticated requests to the control plane using `FORGE_ADMIN_TOKEN`.
- Uses `FORGE_ADMIN_CONSOLE_PASSWORD` for browser login and stores a signed session cookie in the browser.
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
- `FORGE_ADMIN_SESSION_KEY`
- `FORGE_TRUSTED_PROXY_IPS`

The session key is optional; when unset, the console derives a signing key from `FORGE_ADMIN_TOKEN` and `FORGE_ADMIN_CONSOLE_PASSWORD`. `FORGE_TRUSTED_PROXY_IPS` is a comma-separated list of reverse-proxy IPs whose `X-Forwarded-For` header should be trusted for login rate limiting. The control-plane private IP is trusted automatically when `FORGE_CONTROL_PLANE_PRIVATE_IP` is set.

Allowed repositories can be added or removed from the Repositories panel. Repositories configured in the control plane's `FORGE_ALLOWED_REPOS` remain read-only in the UI; repositories added from the UI are stored in the control-plane database.

Private repositories are supported. Store a repo-level credential for one `owner/repo`, or an owner-level credential for `owner` to cover every allowed repo under that owner. Repo-level credentials override owner-level credentials.

The app intentionally has no third-party Python dependencies.
