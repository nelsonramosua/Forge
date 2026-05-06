# AWS Deployment Guide

This guide deploys Forge on AWS while keeping the OCI Terraform stack available for future use.

AWS Free Tier terms changed for accounts created on or after July 15, 2025. Check your own account in **Billing and Cost Management -> Free Tier** before applying. The default profile uses two `t3.micro` instances because it is the conservative smoke-test baseline.

## 1. AWS Account Safety

Before creating resources:

1. Open **Billing and Cost Management -> Budgets**.
2. Create a monthly cost budget, for example `5 USD`.
3. Enable Free Tier usage alerts.
4. Confirm which EC2 instance types are Free Tier eligible in your account.

AWS docs include a CLI filter for eligible EC2 types:

```sh
aws ec2 describe-instance-types \
  --filters Name=free-tier-eligible,Values=true \
  --query "InstanceTypes[*].[InstanceType]" \
  --output text | sort
```

## 2. AWS Credentials

Install and configure the AWS CLI:

```sh
aws configure
```

It writes:

```text
~/.aws/config
~/.aws/credentials
```

Minimum practical IAM permissions for the Terraform user:

- EC2 VPC, subnet, route table, internet gateway, security group, key pair, and instance management.
- IAM read access is not required by this Terraform stack.
- Route 53 change permissions only if `manage_route53 = true`.

Environment-variable alternative:

```sh
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=eu-west-1
```

## 3. SSH Key

Create a dedicated key:

```sh
ssh-keygen -t ed25519 -f ~/.ssh/forge_aws -C forge-aws
chmod 600 ~/.ssh/forge_aws
```

Terraform imports `~/.ssh/forge_aws.pub` as an EC2 key pair.

## 4. Terraform Variables

Create your local tfvars:

```sh
cp infra/terraform/aws/terraform.tfvars.example infra/terraform/aws/terraform.tfvars
```

Edit:

```hcl
aws_region          = "eu-west-1"
base_domain         = "forge.yourdomain.com"
ssh_public_key_path = "/home/YOUR_USER/.ssh/forge_aws.pub"
ssh_private_key_path = "/home/YOUR_USER/.ssh/forge_aws"
admin_cidr          = "YOUR_PUBLIC_IP/32"

control_plane_instance_type = "t3.micro"
worker_instance_type        = "t3.micro"
root_volume_size_gb         = 30

manage_route53  = false
route53_zone_id = ""
```

Find your public IP:

```sh
curl ifconfig.me
```

`admin_cidr` must not be `0.0.0.0/0`. Terraform rejects that value. The public control-plane API port `8080` is not opened; use HTTPS on `443` or SSH into the control-plane VM for local checks.

## 5. Apply AWS Terraform

```sh
terraform -chdir=infra/terraform/aws init
terraform -chdir=infra/terraform/aws fmt -check
terraform -chdir=infra/terraform/aws validate
terraform -chdir=infra/terraform/aws plan
terraform -chdir=infra/terraform/aws apply
```

Terraform creates:

- VPC, public subnet, internet gateway, route table.
- VPC Flow Logs for the Forge VPC, delivered to an encrypted CloudWatch Logs group backed by a customer-managed KMS key for network-visibility and security review.
- Security group for the control plane.
- Control-plane API port `8080` is reachable only from inside the Forge VPC.
  Use Caddy over HTTPS for public webhooks and SSH into the control plane for local admin/metrics checks.
- Security group for the worker; worker inbound is only from the control-plane security group.
- Worker instances do not receive an IAM instance profile, and EC2 metadata is locked to IMDSv2. In the Free Tier topology the worker still has a public IP for outbound package/app dependency downloads without a paid NAT gateway; its security group does not allow public inbound traffic.
- EC2 key pair.
- `forge-control-plane` EC2 instance.
- `forge-worker-1` EC2 instance.
- `infra/ansible/inventory.ini`.

The reference AWS stack keeps the control plane publicly reachable on `80/443` and assigns a public IP to the control-plane instance on purpose, so the repository root `.trivyignore` documents those accepted exceptions for the Trivy scan.

## 6. DNS

Route 53 hosted zones can incur a monthly charge, so DNS automation is disabled by default.

After apply, get the control-plane public IP:

```sh
terraform -chdir=infra/terraform/aws output control_plane_public_ip
```

Create DNS records wherever your domain is hosted:

```text
BASE_DOMAIN      A  CONTROL_PLANE_PUBLIC_IP
*.BASE_DOMAIN    A  CONTROL_PLANE_PUBLIC_IP
```

For example, if Terraform prints:

```text
forge.example.com   A  203.0.113.10
*.forge.example.com A  203.0.113.10
```

create those two records in the DNS provider that currently hosts the domain.
If the domain uses a registrar's default nameservers, create the records there.
If you create a Route 53 hosted zone, update the domain's nameservers at the registrar to the Route 53 nameservers.

Check resolution before using the domain:

```sh
dig +short "$BASE_DOMAIN"
dig +short "myapp.$BASE_DOMAIN"
```

Both should return the control-plane public IP. Until DNS resolves, use the IP directly for smoke tests:

If `dig` times out against a local resolver such as `10.255.255.254`, test against public resolvers before changing DNS records:

```sh
dig @1.1.1.1 +short "$BASE_DOMAIN"
dig @8.8.8.8 +short "$BASE_DOMAIN"
dig @1.1.1.1 +short "myapp.$BASE_DOMAIN"
```

Timeouts against only the local resolver usually mean a local DNS/WSL/VPN issue.
Empty answers from public resolvers usually mean the records are not published yet or the domain's nameservers point somewhere else.

### Cloudflare DNS

For the first end-to-end test, set both records to **DNS only** in Cloudflare, not proxied:

```text
@   A  CONTROL_PLANE_PUBLIC_IP  DNS only
*   A  CONTROL_PLANE_PUBLIC_IP  DNS only
```

To avoid future TLS issuance failures when control-plane IPs change, enable Cloudflare DNS-01 automation in Caddy:

1. Add to `infra/ansible/group_vars/all/main.local.yml`:

```yaml
forge_caddy_dns_cloudflare_enabled: true
```

2. Add `vault_cloudflare_api_token` to `infra/ansible/group_vars/all/vault.yml`.
   The token must include `Zone.Zone:Read` and `Zone.DNS:Edit` for your zone.

3. Re-run the control-plane play:

```sh
ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/forge_aws \
ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml \
  --ask-vault-pass \
  --limit forge-control-plane
```

When this mode is enabled, Caddy uses DNS-01 through Cloudflare for both base-domain and on-demand subdomain certificates, so ACME validation no longer depends on direct public reachability to the origin host during challenge checks.

Forge already uses Caddy on the control-plane VM to serve public HTTPS on port `443` and to reverse-proxy the control-plane API on `127.0.0.1:8080`.
Application subdomains use Caddy On-Demand TLS with an internal Forge allow check, so the first HTTPS request to a new app hostname can take a few seconds while Caddy obtains the certificate.

Use these URLs:

```text
https://BASE_DOMAIN/healthz
https://BASE_DOMAIN/api/v1/webhook/github
https://myapp.BASE_DOMAIN/
```

Do not use this URL:

```text
https://BASE_DOMAIN:8080/healthz
```

Port `8080` is plain HTTP and is intentionally not the public HTTPS entrypoint.

After direct DNS-only mode is working, Cloudflare proxy can be re-enabled, but set **SSL/TLS encryption mode** to `Full (strict)` and confirm Cloudflare can reach the origin on `443`. A Cloudflare `521` means Cloudflare could not connect to the origin web server, commonly because the origin is not listening on the expected port or a firewall blocks Cloudflare.

If public DNS still returns Cloudflare IPs, the records are still proxied:

```sh
dig @1.1.1.1 +short "$BASE_DOMAIN"
```

`104.x.x.x` or `172.x.x.x` answers are Cloudflare edge IPs. In DNS-only mode the answer should be the control-plane public IP.

After switching to DNS-only, reload Caddy on the control plane and verify both HTTP and HTTPS at the origin:

```sh
CONTROL_IP=$(terraform -chdir=infra/terraform/aws output -raw control_plane_public_ip)
BASE_DOMAIN=forge.example.com

ssh -i ~/.ssh/forge_aws ubuntu@$CONTROL_IP \
  'sudo caddy validate --config /etc/caddy/Caddyfile && sudo systemctl restart caddy && sudo journalctl -u caddy -n 80 --no-pager'

curl --resolve "$BASE_DOMAIN:80:$CONTROL_IP" -fsS "http://$BASE_DOMAIN/healthz"
curl --resolve "$BASE_DOMAIN:443:$CONTROL_IP" -fsS "https://$BASE_DOMAIN/healthz"
```

If port `80` returns a Caddy `404`, the active Caddy config does not contain the base-domain reverse proxy route. Re-run the control-plane Ansible play:

```sh
ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/forge_aws \
ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml \
  --ask-vault-pass \
  --limit forge-control-plane
```

```sh
CONTROL_IP=$(terraform -chdir=infra/terraform/aws output -raw control_plane_public_ip)
curl -fsS https://$BASE_DOMAIN/healthz
ssh -i ~/.ssh/forge_aws ubuntu@$CONTROL_IP 'curl -fsS http://127.0.0.1:8080/metrics'
```

If you already have a Route 53 hosted zone and accept the cost:

```hcl
manage_route53  = true
route53_zone_id = "Z..."
```

## 7. Ansible Vault

If you already created the Vault for OCI, reuse it.

Otherwise:

```sh
mkdir -p infra/ansible/group_vars/all
cp infra/ansible/group_vars/all/vault.yml.example /tmp/forge-vault.yml
ansible-vault encrypt /tmp/forge-vault.yml
mv /tmp/forge-vault.yml infra/ansible/group_vars/all/vault.yml
ansible-vault edit infra/ansible/group_vars/all/vault.yml
```

Generate values:

```sh
openssl rand -base64 32   # vault_forge_master_key
openssl rand -hex 32      # vault_forge_agent_token
openssl rand -hex 32      # vault_forge_admin_token
openssl rand -hex 32      # vault_forge_github_webhook_secret
openssl rand -base64 24   # vault_grafana_admin_password
# Cloudflare DNS-01 (optional)
# create token in Cloudflare dashboard with Zone.Zone:Read + Zone.DNS:Edit
```

Configure the repositories Forge may deploy. For local deploy values that should not be committed, create `infra/ansible/group_vars/all/main.local.yml`:

```yaml
forge_allowed_repos:
  - YOUR_GITHUB_USER/forge-e2e-smoke

forge_allowed_branches:
  - main
```

The control plane fails closed if the token/key variables or the repository allowlist are missing. This is intentional: unsigned or unknown webhooks should never trigger builds.

## 8. Run Ansible

```sh
ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/forge_aws \
ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml --ask-vault-pass
```

The worker is reached through the control-plane instance. Use an explicit SSH `ProxyCommand` so the same local private key is used for both SSH hops; do not copy the private key to the control-plane instance!

## 9. Smoke Tests

From your machine:

```sh
CONTROL_IP=$(terraform -chdir=infra/terraform/aws output -raw control_plane_public_ip)
WORKER_PRIVATE_IP=$(terraform -chdir=infra/terraform/aws output -raw worker_private_ip)

curl -fsS http://$CONTROL_IP:9090/-/healthy
curl -fsS http://$CONTROL_IP:3000/api/health
```

If the control-plane API returns an error about `data/forge.db`, rerun Ansible or reload the unit manually. The deployed service must use `FORGE_DB_PATH=/var/lib/forge/forge.db`, not the local development default.

```sh
ssh -i ~/.ssh/forge_aws ubuntu@$CONTROL_IP \
  'sudo systemctl daemon-reload && sudo systemctl restart forge-control-plane && systemctl show forge-control-plane -p Environment'
```

From the control plane:

```sh
ssh -i ~/.ssh/forge_aws ubuntu@$CONTROL_IP
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:2019/config/
curl -fsS http://127.0.0.1:8080/metrics
curl -fsS http://$WORKER_PRIVATE_IP:9108/metrics
systemctl status forge-control-plane caddy prometheus prometheus-alertmanager grafana-server
```

If worker metrics return `502`, the worker exporter is reachable but cannot read the agent's local metrics socket. Redeploy the worker play and verify the socket is owned by `root:forge` with group read/write permissions:

```sh
ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/forge_aws \
ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml \
  --ask-vault-pass \
  --limit forge-worker-1

ssh -i "$SSH_KEY" \
  -o IdentitiesOnly=yes \
  -o ProxyCommand="ssh -i $SSH_KEY -o IdentitiesOnly=yes -W %h:%p ubuntu@$CONTROL_IP" \
  ubuntu@$WORKER_PRIVATE_IP \
  'sudo ls -l /run/forge-agent/metrics.sock && sudo systemctl restart forge-exporter'
```

Forge allocates app host ports from `20000-39999` by default. Apps should bind to `$PORT`; do not hard-code the manifest `run.port` inside the app process. 
See [runtime-model.md](runtime-model.md) for dependency and port isolation guidance.

Worker check:

```sh
SSH_KEY="${HOME}/.ssh/forge_aws"

ssh -i "$SSH_KEY" \
  -o IdentitiesOnly=yes \
  -o ProxyCommand="ssh -i $SSH_KEY -o IdentitiesOnly=yes -W %h:%p ubuntu@$CONTROL_IP" \
  ubuntu@$WORKER_PRIVATE_IP \
  'systemctl status forge-agent forge-exporter'
```

## 10. GitHub End-To-End Test

The first GitHub E2E can use a public repository. Private repositories are supported after you register
an encrypted repo or owner credential through the admin API or admin console:

```sh
curl -X PUT "https://BASE_DOMAIN/api/v1/repos/OWNER/REPO/credential" \
  -H "Authorization: Bearer $FORGE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"token":"github_pat_..."}'
```

Use `/api/v1/repos/OWNER/credential` instead to cover every allowed repo under the same owner/org. A repo-level credential overrides an owner-level credential. The repository still must be allowed: either listed in `forge_allowed_repos` at startup or added later through the admin console/API.

The bundled admin console lives in `examples/forge-admin`. To deploy it, publish that repository, set `FORGE_ADMIN_APP_REPO` (or `forge_admin_app_repo` in Ansible vars) to that repo, and deploy an app whose `forge.yaml` uses `name: admin`.

Confirm public DNS and TLS first:

```sh
BASE_DOMAIN=forge.example.com
CONTROL_IP=$(terraform -chdir=infra/terraform/aws output -raw control_plane_public_ip)

dig @1.1.1.1 +short "$BASE_DOMAIN"
dig @1.1.1.1 +short "myapp.$BASE_DOMAIN"
curl -fsS "https://$BASE_DOMAIN/healthz"
```

If local DNS is still broken but public DNS is correct, use `--resolve` for local smoke tests:

```sh
curl --resolve "$BASE_DOMAIN:443:$CONTROL_IP" -fsS "https://$BASE_DOMAIN/healthz"
```

Create a public smoke-test app repository:

```sh
mkdir -p /tmp/forge-e2e-smoke
cp -R examples/smoke-app/. /tmp/forge-e2e-smoke/
cd /tmp/forge-e2e-smoke
git init -b main
git add app.py forge.yaml
git commit -m "Initial Forge smoke app"
```

Create a public GitHub repository, then push this app. With `gh`:

```sh
gh repo create forge-e2e-smoke --public --source=. --remote=origin --push
```

Or create an empty public repository in the GitHub UI and run:

```sh
git remote add origin git@github.com:YOUR_USER/forge-e2e-smoke.git
git push -u origin main
```

Get the webhook secret from Ansible Vault:

```sh
ansible-vault view infra/ansible/group_vars/all/vault.yml
```

Use only `vault_forge_github_webhook_secret` as the GitHub webhook secret.

In the GitHub repository, create a webhook:

- Payload URL: `https://YOUR_BASE_DOMAIN/api/v1/webhook/github`.
- Content type: `application/json`.
- Secret: `vault_forge_github_webhook_secret`.
- SSL verification: enabled.
- Events: just `push`.
- Active: enabled.

GitHub sends a `ping` delivery when the webhook is created. 
The Forge control plane answers that ping without deploying; the first real deployment comes from a `push` event on an allowed branch.

Watch live Forge events in one terminal:

```sh
ADMIN_TOKEN="$(ansible-vault view infra/ansible/group_vars/all/vault.yml | awk -F': ' '/vault_forge_admin_token/ {gsub(/\"/, \"\", $2); print $2}')"
curl -N -H "Authorization: Bearer $ADMIN_TOKEN" "https://$BASE_DOMAIN/api/v1/events"
```

Trigger a deployment in the *app* repository:

```sh
cd /tmp/forge-e2e-smoke
date -u +"%FT%TZ" > trigger.txt
git add trigger.txt
git commit -m "Trigger Forge deployment"
git push
```

Expected lifecycle:

```text
pending -> building -> deploying -> running.
```

Validate the deployed app:

```sh
curl -fsS "https://myapp.$BASE_DOMAIN/health"
curl -fsS "https://myapp.$BASE_DOMAIN/"
```

If local DNS is the only broken part:

```sh
curl --resolve "myapp.$BASE_DOMAIN:443:$CONTROL_IP" -fsS "https://myapp.$BASE_DOMAIN/"
```

For logs while testing:

```sh
ssh -i ~/.ssh/forge_aws ubuntu@$CONTROL_IP \
  'journalctl -u forge-control-plane -n 100 --no-pager'

ssh -i "$SSH_KEY" \
  -o IdentitiesOnly=yes \
  -o "ProxyCommand=ssh -i $SSH_KEY -o IdentitiesOnly=yes -W %h:%p ubuntu@$CONTROL_IP" \
  ubuntu@$WORKER_PRIVATE_IP \
  'journalctl -u forge-agent -n 200 --no-pager'
```

## 11. Cleanup

To avoid costs, you may want to:

```sh
terraform -chdir=infra/terraform/aws destroy
```

Also check, then:

- EC2 instances terminated.
- EBS volumes deleted.
- Elastic IPs none allocated.
- Route 53 records/hosted zones if you enabled them.
