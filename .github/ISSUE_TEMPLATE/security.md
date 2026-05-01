---
name: Security vulnerability
about: Report a security issue — please read before opening
labels: security
---

> [!CAUTION]
> **Do not include exploit details, credentials, or sensitive reproduction steps in a public issue.**
>
> If the vulnerability is exploitable against a running Forge instance (e.g. auth bypass, SSRF, RCE, secret disclosure), please report it privately via GitHub's
> [private vulnerability reporting](../../security/advisories/new) instead of opening a public issue.
>
> Use this template only for lower-severity findings (e.g. information disclosure, hardening gaps, dependency advisories) where public discussion is safe.

## Summary

<!-- One or two sentences describing the issue without including exploit details. -->

## Affected component

<!-- control-plane / forge-agent / forge-build-runner / Terraform infra / Ansible / other -->

## Severity assessment

<!-- Your assessment of impact and exploitability. -->

- CVSS score (if known):
- Requires authenticated access: Yes / No
- Requires `AllowLocalRepos=true` or other non-default config: Yes / No

## Suggested mitigation

<!-- If you have a fix or workaround in mind, describe it here. -->
