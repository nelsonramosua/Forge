output "control_plane_public_ip" {
  value = oci_core_instance.control_plane.public_ip
}

output "control_plane_private_ip" {
  value = oci_core_instance.control_plane.private_ip
}

output "worker_private_ip" {
  value = oci_core_instance.worker.private_ip
}

output "worker_public_ip" {
  value = oci_core_instance.worker.public_ip
}

output "ansible_inventory" {
  value = local_file.ansible_inventory.filename
}

output "dns_zone_id" {
  value = try(oci_dns_zone.forge[0].id, var.existing_dns_zone_id)
}

output "dns_zone_transfer_servers" {
  description = "OCI DNS zone transfer server addresses. Use the OCI Console zone page for authoritative registrar nameserver delegation if needed."
  value       = try([for server in oci_dns_zone.forge[0].zone_transfer_servers : server.address], [])
}
