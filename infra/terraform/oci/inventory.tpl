[control_plane]
forge-control-plane ansible_host=${control_plane_public_ip} ansible_user=ubuntu forge_base_domain=${base_domain} forge_control_plane_private_ip=${control_plane_private_ip}

[workers]
forge-worker-1 ansible_host=${worker_private_ip} ansible_user=ubuntu ansible_ssh_common_args='-o ProxyJump=ubuntu@${control_plane_public_ip}' forge_control_plane_url=http://${control_plane_private_ip}:8080 forge_agent_address=${worker_private_ip}

[forge:children]
control_plane
workers
