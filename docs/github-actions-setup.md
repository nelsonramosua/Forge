# GitHub Actions secrets and variables

Forge uses GitHub Actions for deployment, infrastructure drift checks, and
maintenance automation. Configure these values in the repository or environment
settings before running the deployment workflows.

Do not commit real IP addresses, account IDs, private keys, tokens, or passwords
to the repository.

## Shared values

| Name | Type | Used by | Description | Example format |
| --- | --- | --- | --- | --- |
| `AWS_REGION` | Variable preferred, secret accepted | `deploy.yml`, `drift-check.yml` | AWS region for Terraform, AWS API calls, and OIDC sessions. | `eu-west-1` |
| `FORGE_BASE_DOMAIN` | Secret | `deploy.yml`, `drift-check.yml` | Base domain used for Forge routes and public endpoints. | `forge.example.com` |
| `FORGE_ADMIN_CIDR` | Secret | `drift-check.yml` | CIDR allowed to reach admin/observability ports. | `203.0.113.10/32` |
| `FORGE_SSH_PUBLIC_KEY` | Secret | `drift-check.yml` | Public SSH key expected by Terraform for EC2 access. | `ssh-ed25519 AAAA... forge-admin` |

## Deploy workflow

These values are required by `.github/workflows/deploy.yml`.

| Name | Type | Description | Example format |
| --- | --- | --- | --- |
| `AWS_DEPLOY_ROLE_ARN` | Secret | AWS IAM role assumed by GitHub Actions through OIDC for deploy operations. | `arn:aws:iam::123456789012:role/forge-deploy` |
| `FORGE_CONTROL_PLANE_IP` | Secret | Public control-plane IP used for SSH and health checks. | `203.0.113.20` |
| `FORGE_CONTROL_PLANE_PRIVATE_IP` | Secret | Private control-plane IP reachable from workers. | `10.0.1.10` |
| `FORGE_CONTROL_PLANE_SECURITY_GROUP_ID` | Secret | Security group temporarily opened for the GitHub Actions runner. | `sg-0123456789abcdef0` |
| `FORGE_WORKER_IP` | Secret | Private worker IP used by the Ansible inventory when deploying workers. | `10.0.2.10` |
| `FORGE_SSH_PRIVATE_KEY` | Secret | Private SSH key for the `ubuntu` user on Forge hosts. Store with escaped or literal newlines. | `-----BEGIN OPENSSH PRIVATE KEY-----...` |
| `ANSIBLE_VAULT_PASSWORD` | Secret | Password used to decrypt Ansible vault values during deployment. | `long-random-string` |

### Runtime and admin secrets

`deploy.yml` passes these secrets into the Ansible playbook as vault values:

| Name | Type | Description | Example format |
| --- | --- | --- | --- |
| `FORGE_MASTER_KEY` | Secret | Master key used by Forge for encrypted secret storage. | 32+ random bytes/base64 text |
| `FORGE_AGENT_TOKEN` | Secret | Token used by agents to authenticate with the control plane. | `fg_agent_...` |
| `FORGE_ADMIN_TOKEN` | Secret | Admin API token used by deployment smoke checks and operators. | `fg_admin_...` |
| `FORGE_GITHUB_WEBHOOK_SECRET` | Secret | HMAC secret for GitHub push webhooks. | `long-random-string` |
| `FORGE_ALLOWED_REPOS` | Secret | Optional newline/comma-separated allowlist of deployable repositories. | `owner/repo` |
| `GRAFANA_ADMIN_PASSWORD` | Secret | Grafana admin password for observability. | `long-random-string` |
| `CLOUDFLARE_API_TOKEN` | Secret | Optional token used when Cloudflare DNS automation is enabled. | `cf_...` |

## Drift-check workflow

These values are used by `.github/workflows/drift-check.yml`.

| Name | Type | Description | Example format |
| --- | --- | --- | --- |
| `AWS_DRIFT_ROLE_ARN` | Secret | Preferred AWS IAM role assumed through OIDC for drift checks. | `arn:aws:iam::123456789012:role/forge-drift` |
| `AWS_ACCESS_KEY_ID` | Secret | Legacy fallback credential when `AWS_DRIFT_ROLE_ARN` is not set. Prefer OIDC where possible. | `AKIA...` |
| `AWS_SECRET_ACCESS_KEY` | Secret | Legacy fallback secret paired with `AWS_ACCESS_KEY_ID`. Prefer OIDC where possible. | `...` |

The drift workflow also uses the shared values listed above:

- `AWS_REGION`
- `FORGE_BASE_DOMAIN`
- `FORGE_ADMIN_CIDR`
- `FORGE_SSH_PUBLIC_KEY`

## Setup checklist

1. Add repository variables first, starting with `AWS_REGION`.
2. Add deploy and drift IAM role ARNs as secrets.
3. Add host IPs, security group IDs, and SSH keys only after infrastructure is
   provisioned.
4. Add Forge runtime/admin secrets before the first deployment.
5. Run the deploy workflow manually and verify the GitHub Actions summary.
6. Run the drift-check workflow after deployment to confirm Terraform state and
   live infrastructure still match.
