---
name: Bug report
about: Something is broken or behaving unexpectedly
labels: bug
---

## What happened

<!-- A clear description of the bug. What did you expect to happen? What actually happened? -->

## How to reproduce

<!-- Steps to reproduce the behaviour. The more specific, the better. -->

1.
2.
3.

## Environment

| Field | Value |
|---|---|
| Component | <!-- control-plane / forge-agent / forge-build-runner / infra / other --> |
| OS / distro | |
| Go version | <!-- `go version` --> |
| GCC version | <!-- `gcc --version` --> |
| Deployment target | <!-- AWS / OCI / local --> |

## Relevant logs

<!-- Paste the relevant section of `journalctl -u forge-control-plane` or `journalctl -u forge-agent`.
     Wrap in ``` fences. Redact tokens, IPs, and secrets before posting! -->

```
```

## forge.yaml (if relevant)

<!-- Paste your forge.yaml, with secrets removed! -->

```yaml
```

## Additional context

<!-- Anything else that might help: Terraform version, Ansible version, whether this worked before, etc. -->
