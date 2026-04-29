[control_plane]
forge-control-plane ansible_host=${control_plane_public_ip} ansible_user=ubuntu ansible_ssh_private_key_file=${ssh_private_key_path} forge_base_domain=${base_domain} forge_control_plane_private_ip=${control_plane_private_ip}

[workers]
forge-worker-1 ansible_host=${worker_private_ip} ansible_user=ubuntu ansible_ssh_private_key_file=${ssh_private_key_path} ansible_ssh_common_args='-o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o ProxyCommand="ssh -i ${ssh_private_key_path} -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -W %h:%p ubuntu@${control_plane_public_ip}"' forge_control_plane_url=http://${control_plane_private_ip}:8080 forge_agent_address=${worker_private_ip}

[forge:children]
control_plane
workers
