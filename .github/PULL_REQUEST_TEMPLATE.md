## What does this PR do?

<!-- A concise description of the change. If it fixes an issue, link it: "Fixes #N". -->

## Motivation

<!-- Why is this change needed? What problem does it solve? -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor / cleanup (no behaviour change)
- [ ] Security fix
- [ ] Documentation / comments only
- [ ] Infrastructure (Terraform / Ansible)
- [ ] Build / tooling

## Implementation notes

<!-- Anything non-obvious about the approach. Design decisions, trade-offs, rejected alternatives.
     For C changes: memory ownership, error paths, any new syscalls used.
     For Go changes: concurrency model, DB schema changes, new env vars. -->

## Testing

<!-- How did you verify this? -->

- [ ] `make test` passes
- [ ] `make build` passes (both C and Go)
- [ ] `scripts/e2e-local.sh` passes
- [ ] `scripts/check-iac.sh` passes (if infra changed)
- [ ] Tested against a live deployment on AWS / OCI
- [ ] Added or updated unit tests

<!-- If you couldn't test something locally, explain why. -->

## forge.yaml compatibility

- [ ] This change does not alter the `forge.yaml` schema
- [ ] This change adds new optional fields (backward compatible)
- [ ] This change alters existing fields — migration path described below

<!-- If schema changed, describe what existing forge.yaml files need to update. -->

## Checklist

- [ ] Self-reviewed the diff — no debug prints, commented-out code, or TODOs left in
- [ ] No secrets, tokens, IP addresses, or personal data in the diff
- [ ] `FORGE_*` env vars documented in README if new ones were added
- [ ] New API endpoints added to the API table in README
