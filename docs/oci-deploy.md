# OCI Deployment Guide

This guide deploys Forge on Oracle Cloud Infrastructure Always Free with two Ampere A1 instances.

## 1. Local Credentials

Configure the OCI Terraform provider locally. Keep these values out of git:

```sh
export OCI_TENANCY_OCID=...
export OCI_USER_OCID=...
export OCI_FINGERPRINT=...
export OCI_PRIVATE_KEY_PATH=~/.oci/oci_api_key.pem
export OCI_REGION=eu-madrid-1
export TF_VAR_compartment_ocid=...
```

Create a dedicated SSH key:

```sh
ssh-keygen -t ed25519 -f ~/.ssh/forge_oci -C forge-oci
```

## 2. Terraform

The OCI Terraform stack lives in `infra/terraform/oci`.

Create local variables:

```sh
cp infra/terraform/oci/terraform.tfvars.example infra/terraform/oci/terraform.tfvars
```

Edit `terraform.tfvars`, then run:

```sh
cd infra/terraform/oci
terraform init
terraform fmt -check
terraform validate
terraform apply
```

Terraform creates:

- VCN with public and private subnets.
- No NAT gateway by default. The worker receives a public IP for outbound package downloads, but inbound traffic is still restricted by OCI security lists.
- `forge-control-plane` with public IP.
- `forge-worker-1` reachable by the control plane on its private IP.
- Optional OCI DNS zone and `A` records for `base_domain` and `*.base_domain`.
- `infra/ansible/inventory.ini`.

If your tenancy has OCI DNS zone quota, enable DNS management in `terraform.tfvars`:

```hcl
create_dns_zone    = true
manage_dns_records = true
```

If you already have an OCI DNS zone, use it instead:

```hcl
create_dns_zone      = false
manage_dns_records   = true
existing_dns_zone_id = "ocid1.dns-zone.oc1..."
```

If the domain is registered outside OCI, delegate it to the OCI DNS nameservers shown in the OCI Console for the created zone. If your tenancy has no DNS zone quota, keep `manage_dns_records = false` and create DNS records manually at your registrar/DNS provider:

```text
BASE_DOMAIN      A  CONTROL_PLANE_PUBLIC_IP
*.BASE_DOMAIN    A  CONTROL_PLANE_PUBLIC_IP
```

`admin_cidr` must be your own narrow IPv4 CIDR, typically `YOUR_PUBLIC_IP/32`.
Terraform rejects `0.0.0.0/0`. The control-plane API port `8080` is only opened inside the VCN; use public HTTPS on `443` or SSH into the control-plane VM for local checks.

## 2.1 Common OCI Free Tier Failures

`NAT gateway limit per VCN reached`:

- Keep `create_nat_gateway = false`.
- Keep `worker_assign_public_ip = true`.
- Re-run `terraform apply`; Terraform will no longer try to create a NAT gateway.

`global-zone-count` exceeded:

- Keep `create_dns_zone = false` and `manage_dns_records = false`.
- Manage DNS manually outside OCI, or delete an unused OCI DNS zone and enable DNS later.

`Out of host capacity` for `VM.Standard.A1.Flex`:

- This is OCI capacity, not a Terraform syntax problem.
- Try another `availability_domain` from the OCI Console if the region has more than one.
- Retry later; A1 capacity frequently fluctuates.
- First try the minimum A1 profile:

```sh
terraform -chdir=infra/terraform/oci apply \
  -var-file=terraform.tfvars \
  -var-file=capacity-a1-min.tfvars.example
```

- If the control plane is the part failing, try AMD Micro for control plane and A1 minimum for worker:

```sh
terraform -chdir=infra/terraform/oci apply \
  -var-file=terraform.tfvars \
  -var-file=capacity-e2-control-a1-worker.tfvars.example
```

- Last resort for network/SSH smoke only, try two AMD Micro instances:

```sh
terraform -chdir=infra/terraform/oci apply \
  -var-file=terraform.tfvars \
  -var-file=capacity-e2-smoke.tfvars.example
```

The E2 Micro profile is not recommended for the full platform because each VM has only 1 GB RAM. Use it to prove Terraform/networking while waiting for A1 capacity.

If both E2 Micro and A1 return `Out of host capacity`, your home region currently has no Always Free compute capacity available for these shapes. You can keep retrying manually, or run:

```sh
OCI_CAPACITY_RETRY_SECONDS=300 \
OCI_CAPACITY_MAX_ATTEMPTS=0 \
scripts/oci-capacity-loop.sh capacity-e2-control-a1-worker.tfvars.example
```

`OCI_CAPACITY_MAX_ATTEMPTS=0` means retry forever. Stop with `Ctrl+C`.

If `terraform apply` partially created resources before failing, do not manually delete things first. Update `terraform.tfvars`, then run:

```sh
terraform -chdir=infra/terraform/oci plan
terraform -chdir=infra/terraform/oci apply
```

## 3. Secrets With Ansible Vault

Create the encrypted vault:

```sh
mkdir -p infra/ansible/group_vars/all
cp infra/ansible/group_vars/all/vault.yml.example /tmp/forge-vault.yml
ansible-vault encrypt /tmp/forge-vault.yml
mv /tmp/forge-vault.yml infra/ansible/group_vars/all/vault.yml
```

Generate values:

```sh
openssl rand -base64 32   # vault_forge_master_key
openssl rand -hex 32      # vault_forge_agent_token
openssl rand -hex 32      # vault_forge_admin_token
openssl rand -hex 32      # vault_forge_github_webhook_secret
openssl rand -base64 24   # vault_grafana_admin_password
```

Edit the vault:

```sh
ansible-vault edit infra/ansible/group_vars/all/vault.yml
```

Configure the GitHub repositories Forge may deploy in a local, ignored
`infra/ansible/group_vars/all/main.local.yml`:

```yaml
forge_allowed_repos:
  - YOUR_GITHUB_USER/forge-e2e-smoke

forge_allowed_branches:
  - main
```

The control plane intentionally refuses to start without secrets and at least one allowed repository.

The bundled admin console lives in `examples/forge-admin`. To deploy it on OCI, publish that repository, set `forge_admin_app_repo` in your Ansible vars to that repo, and deploy an app whose `forge.yaml` uses `name: admin`. Private repository credentials are still registered through the control plane admin API after the deploy is up.

## 4. Ansible

Run Ansible through the generated inventory. The worker is reached via SSH ProxyJump through the control-plane VM.

```sh
ANSIBLE_PRIVATE_KEY_FILE=~/.ssh/forge_oci \
ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml --ask-vault-pass
```

## 5. Smoke Tests

From your machine:

```sh
curl -fsS https://BASE_DOMAIN/healthz
curl -fsS http://CONTROL_PLANE_PUBLIC_IP:9090/-/healthy
curl -fsS http://CONTROL_PLANE_PUBLIC_IP:3000/api/health
```

From the control-plane VM:

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:2019/config/
curl -fsS http://127.0.0.1:8080/metrics
curl -fsS http://WORKER_PRIVATE_IP:9108/metrics
```

Watch services:

```sh
systemctl status forge-control-plane caddy prometheus prometheus-alertmanager grafana-server
ssh -J ubuntu@CONTROL_PLANE_PUBLIC_IP ubuntu@WORKER_PRIVATE_IP 'systemctl status forge-agent forge-exporter'
```

## 6. GitHub Webhook

Create a public test repository with `examples/forge.yaml`. Configure GitHub webhook:

- Payload URL: `https://BASE_DOMAIN/api/v1/webhook/github`
- Content type: `application/json`
- Secret: `vault_forge_github_webhook_secret`
- Event: push

Watch events:

```sh
curl -N -H "Authorization: Bearer ADMIN_TOKEN" https://BASE_DOMAIN/api/v1/events
```
