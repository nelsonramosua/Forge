# Runbook: Worker Node Failure

## Symptoms

- `ForgeNoOnlineAgents` alert fires.
- `/metrics` on the control plane reports `forge_agents_online 0`.
- Pending deployments remain in `pending` and no build tasks are claimed.
- Existing running deployments may show unhealthy observations such as `assigned agent is offline`, but they should not be marked failed solely because the agent missed heartbeats.

## Triage

1. Check control-plane logs for heartbeat failures or scheduler errors:
   `journalctl -u forge-control-plane -n 200 --no-pager`
2. Check each worker service:
   `systemctl status forge-agent forge-exporter`
3. Verify the worker can reach the control plane:
   `curl -fsS http://CONTROL_PLANE_IP:8080/healthz`
4. Inspect cgroup and namespace permissions if builds are failing:
   `journalctl -u forge-agent -n 200 --no-pager`

## Recovery

1. Restart the agent:
   `systemctl restart forge-agent`
2. If the node is unavailable, provision a replacement worker with Terraform and rerun Ansible:
   `terraform apply`
   `ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml`
3. Re-send the GitHub webhook or push a no-op commit for deployments that stayed pending longer than the SLO window.

## Prevention

- Keep at least two workers online for production.
- Alert on disk pressure and memory pressure before the agent stops heartbeating.
- Keep the agent token and control-plane URL in `/etc/forge/agent.env` managed by configuration management.
