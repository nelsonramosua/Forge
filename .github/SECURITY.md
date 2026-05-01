# Security Policy

## Reporting a vulnerability

**Do not open a public issue for security vulnerabilities.**

If you find something that could be exploited against a running Forge instance — authentication bypass, SSRF, RCE, secret disclosure, privilege escalation — please report it privately via [GitHub's private vulnerability reporting](https://github.com/nelsonramosua/Forge/security/advisories/new).

Include as much detail as you can:

- A description of the vulnerability and its impact
- Steps to reproduce or a proof-of-concept (redact any real credentials)
- Which component is affected (`forge-agent`, `control-plane`, `forge-build-runner`, Terraform, etc.)
- Your assessment of exploitability (requires valid agent token, requires `AllowLocalRepos=true`, etc.)

I aim to acknowledge reports within **72 hours** and to provide a timeline for a fix within **7 days**.

## Scope

The following are **in scope**:

- Authentication and authorisation in the control plane API.
- HMAC verification of GitHub webhooks.
- Secret encryption and key handling (`vault` package).
- SSRF or path traversal via `FORGE_ALLOW_LOCAL_REPOS`.
- Namespace or cgroup escape in `forge-build-runner`.
- Privilege escalation from the agent to the host.
- Information disclosure via API responses or logs.

The following are **out of scope**:

- Attacks that require physical access to the host.
- Issues in dependencies that have no available fix.
- Self-XSS or issues that require the attacker to already have admin-level Forge credentials.
- `--require-isolation=false` builds (isolation is intentionally disabled in that mode).

## Supported versions

Only the latest commit on `main` is supported. There are no versioned releases with backport guarantees.

## Security-relevant configuration

A correctly hardened Forge deployment should have:

- `FORGE_ALLOW_LOCAL_REPOS` unset or `false` in production.
- `FORGE_REQUIRE_ISOLATION=true` on all workers.
- Agent and admin tokens generated with a cryptographically secure RNG (`openssl rand -hex 32`).
- `FORGE_MASTER_KEY` of at least 32 bytes, generated with `openssl rand -base64 32`.
- Workers in a private subnet with no public IP.
- Caddy Admin API (`:2019`) accessible only from localhost.
- Prometheus and Grafana ports accessible only from a trusted CID.