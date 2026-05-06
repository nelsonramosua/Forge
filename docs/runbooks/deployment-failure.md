# Runbook: Deployment Failure

## Symptoms

- A deployment enters `failed`.
- `ForgeDeploymentFailures` alert fires.
- SSE logs show build, run, or health-check errors.

## Triage

1. List recent deployments:
   `curl -H "Authorization: Bearer $FORGE_ADMIN_TOKEN" http://CONTROL_PLANE:8080/api/v1/deployments`
2. Watch live events:
   `curl -N -H "Authorization: Bearer $FORGE_ADMIN_TOKEN" http://CONTROL_PLANE:8080/api/v1/events`
3. Check worker logs:
   `journalctl -u forge-agent -n 300 --no-pager`
4. Verify the app's `forge.yaml` has valid build commands, run command, port, health path, and resource limits.

## Recovery

1. Fix the application repository or secret values.
2. Store missing secrets:
   `curl -X PUT -H "Authorization: Bearer $FORGE_ADMIN_TOKEN" -H "Content-Type: application/json" -d '{"value":"..."}' http://CONTROL_PLANE:8080/api/v1/apps/APP/secrets/KEY`
3. If the deployment was marked failed with retryable state, requeue it directly:
   `curl -X POST -H "Authorization: Bearer $FORGE_ADMIN_TOKEN" http://CONTROL_PLANE:8080/api/v1/deployments/DEPLOYMENT_ID/retry`
4. To redeploy without a new commit, use the admin console's Redeploy HEAD action or call `POST /api/v1/deployments` with the repo and branch.
5. To return to a previous commit, use the admin console's Rollback action or call `POST /api/v1/deployments/DEPLOYMENT_ID/rollback`.
6. Push a new commit to trigger a fresh deployment when the fix lives in source control.

## Prevention

- Keep health checks cheap and deterministic.
- Prefer explicit runtime versions in `forge.yaml`.
- Set memory and CPU limits high enough for build spikes, not only steady-state runtime.
- Expect route rollback on failed health reconciliation; verify the latest successful deployment is still running before re-pushing.
