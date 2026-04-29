output "control_plane_public_ip" {
  value = aws_instance.control_plane.public_ip
}

output "control_plane_private_ip" {
  value = aws_instance.control_plane.private_ip
}

output "worker_public_ip" {
  value = aws_instance.worker.public_ip
}

output "worker_private_ip" {
  value = aws_instance.worker.private_ip
}

output "ansible_inventory" {
  value = local_file.ansible_inventory.filename
}

output "manual_dns_records" {
  value = {
    base_domain     = "${var.base_domain} A ${aws_instance.control_plane.public_ip}"
    wildcard_domain = "*.${var.base_domain} A ${aws_instance.control_plane.public_ip}"
    route53_managed = var.manage_route53 && var.route53_zone_id != ""
  }
}
