# Forge SLOs

## Service Level Indicators

- Build latency: time from GitHub webhook acceptance to build task completion.
- Deploy latency: time from webhook acceptance to deployment reaching `running`.
- Deploy success rate: percentage of deployments that reach `running` without entering `failed`.
- Agent availability: percentage of scheduler ticks with at least one online worker.
- Proxy update latency: time from run task completion to Caddy accepting the route config.

## Initial Objectives

- 95% of builds complete in under 10 minutes.
- 99% of deployments either reach `running` or `failed` in under 15 minutes.
- 99% deployment success rate for commits whose build and health commands are valid.
- 99.5% agent availability for at least one worker per control plane.
- 99% of Caddy route updates complete in under 5 seconds when the Caddy Admin API is available.

## Error Budget Policy

When deploy success rate drops below objective over a rolling 7-day window, pause feature work on scheduler, runner, and proxy code until failed deployment causes have a runbook entry, a regression test, or an alert rule.
