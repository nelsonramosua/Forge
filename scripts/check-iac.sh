#!/usr/bin/env bash
set -euo pipefail

if ! command -v terraform >/dev/null 2>&1; then
  echo "Skipping Terraform checks: terraform is not installed."
else
  terraform -chdir=infra/terraform/oci fmt -check
  terraform -chdir=infra/terraform/aws fmt -check

  if [[ -d infra/terraform/oci/.terraform ]]; then
    terraform -chdir=infra/terraform/oci validate
  else
    echo "Skipping OCI terraform validate: run 'terraform -chdir=infra/terraform/oci init' first."
  fi

  if [[ -d infra/terraform/aws/.terraform ]]; then
    terraform -chdir=infra/terraform/aws validate
  else
    echo "Skipping AWS terraform validate: run 'terraform -chdir=infra/terraform/aws init' first."
  fi
fi

if [[ -f infra/ansible/inventory.ini && -f infra/ansible/group_vars/all/vault.yml ]]; then
  if command -v ansible-playbook >/dev/null 2>&1; then
    ANSIBLE_LOCAL_TEMP="${ANSIBLE_LOCAL_TEMP:-/tmp/ansible-local}" \
    ANSIBLE_REMOTE_TEMP="${ANSIBLE_REMOTE_TEMP:-/tmp/ansible-remote}" \
      ansible-playbook -i infra/ansible/inventory.ini infra/ansible/playbook.yml --syntax-check --ask-vault-pass
  else
    echo "Skipping ansible syntax-check: ansible-playbook is not installed."
  fi
else
  echo "Skipping ansible syntax-check: generated inventory and encrypted vault are required."
fi
