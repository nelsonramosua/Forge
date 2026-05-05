# Forge Test Plan

## Local

```sh
make clean
make build
make test
go test -race ./...
go vet ./...
./bin/forge-build-runner --workdir /tmp --cgroup smoke -- /bin/sh -lc /bin/true
./bin/forge-build-runner --require-isolation --workdir /tmp --cgroup strict-smoke -- /bin/sh -lc /bin/true
```

Run the local end-to-end harness:

```sh
scripts/e2e-local.sh
```

The local E2E test starts a temporary control plane and agent, creates a temporary Git repo with `forge.yaml`, sends a signed GitHub-style webhook, and waits for the deployment to become `running`. It also exercises the current port-assignment and route-reconciliation path.

Security checks covered by unit/E2E tests:

- Control-plane startup fails if required tokens, webhook secret, master key, or repository allowlist are missing.
- GitHub webhooks require `X-Hub-Signature-256`, `X-GitHub-Event: push`, an allowed `owner/repo`, an allowed branch, and a hex commit SHA.
- Deployments use dynamically allocated app ports from `20000-39999`.
- `forge.yaml` parsing rejects unknown fields and accepts normal quoted YAML values.
- Production worker builds require Linux namespace isolation instead of falling back to plain `fork()`.

## Infrastructure

Terraform stacks are split by cloud:

```sh
terraform -chdir=infra/terraform/oci fmt -check
terraform -chdir=infra/terraform/oci validate
terraform -chdir=infra/terraform/aws fmt -check
terraform -chdir=infra/terraform/aws validate
```

`terraform validate` requires `terraform init` and valid provider configuration for each cloud.
If OCI returns NAT or DNS quota errors, use the Free Tier defaults documented in [oci-deploy.md](oci-deploy.md): `create_nat_gateway=false`, `worker_assign_public_ip=true`, `create_dns_zone=false`, and `manage_dns_records=false`.
If OCI returns `Out of host capacity`, try `infra/terraform/oci/capacity-a1-min.tfvars.example`, then `capacity-e2-control-a1-worker.tfvars.example`.
If both profiles fail, use `scripts/oci-capacity-loop.sh` to retry periodically.

## OCI Post-Deploy

```sh
curl -fsS https://BASE_DOMAIN/healthz
curl -fsS http://CONTROL_PLANE_PUBLIC_IP:9090/-/healthy
curl -fsS http://CONTROL_PLANE_PUBLIC_IP:3000/api/health
ssh ubuntu@CONTROL_PLANE_PUBLIC_IP 'curl -fsS http://127.0.0.1:8080/healthz'
ssh ubuntu@CONTROL_PLANE_PUBLIC_IP 'curl -fsS http://127.0.0.1:2019/config/'
ssh ubuntu@CONTROL_PLANE_PUBLIC_IP 'curl -fsS http://WORKER_PRIVATE_IP:9108/metrics'
```

## Failure Cases

- Missing app secret: deployment should fail or app should fail health checks, and SSE should show the app error.
- Invalid build command: build task should enter `failed`.
- Invalid health path: run task should enter `failed`.
- Stopped worker: new deployments remain `pending` until an agent comes back online.
- Unsigned webhook: request should return `401`.
- Repo not in `FORGE_ALLOWED_REPOS`: request should return `403`.
- Unsafe commit SHA such as `--help`: request should return `400`.
