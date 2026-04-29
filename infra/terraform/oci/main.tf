locals {
  availability_domain = var.availability_domain != "" ? var.availability_domain : data.oci_identity_availability_domains.available.availability_domains[0].name
  common_tags = {
    Project = "Forge"
  }
}

data "oci_identity_availability_domains" "available" {
  compartment_id = var.compartment_ocid
}

data "oci_core_images" "ubuntu_a1" {
  compartment_id           = var.compartment_ocid
  operating_system         = var.image_operating_system
  operating_system_version = var.image_operating_system_version
  shape                    = var.worker_shape
  state                    = "AVAILABLE"
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"
}

data "oci_core_images" "control_plane" {
  compartment_id           = var.compartment_ocid
  operating_system         = var.image_operating_system
  operating_system_version = var.image_operating_system_version
  shape                    = var.control_plane_shape
  state                    = "AVAILABLE"
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"
}

resource "oci_core_vcn" "forge" {
  compartment_id = var.compartment_ocid
  cidr_block     = var.vcn_cidr
  display_name   = "forge-vcn"
  dns_label      = "forge"
  freeform_tags  = local.common_tags
}

resource "oci_core_internet_gateway" "forge" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-internet-gateway"
  enabled        = true
  freeform_tags  = local.common_tags
}

resource "oci_core_nat_gateway" "forge" {
  count          = var.create_nat_gateway ? 1 : 0
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-nat-gateway"
  freeform_tags  = local.common_tags
}

resource "oci_core_route_table" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-public-routes"
  freeform_tags  = local.common_tags

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.forge.id
  }
}

resource "oci_core_route_table" "private" {
  count          = var.create_nat_gateway ? 1 : 0
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-private-routes"
  freeform_tags  = local.common_tags

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_nat_gateway.forge[0].id
  }
}

resource "oci_core_security_list" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-public-security"
  freeform_tags  = local.common_tags

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  ingress_security_rules {
    description = "SSH from admin network"
    protocol    = "6"
    source      = var.admin_cidr

    tcp_options {
      min = 22
      max = 22
    }
  }

  ingress_security_rules {
    description = "HTTP"
    protocol    = "6"
    source      = "0.0.0.0/0"

    tcp_options {
      min = 80
      max = 80
    }
  }

  ingress_security_rules {
    description = "HTTPS"
    protocol    = "6"
    source      = "0.0.0.0/0"

    tcp_options {
      min = 443
      max = 443
    }
  }

  ingress_security_rules {
    description = "Control-plane API from admin network"
    protocol    = "6"
    source      = var.admin_cidr

    tcp_options {
      min = 8080
      max = 8080
    }
  }

  ingress_security_rules {
    description = "Prometheus from admin network"
    protocol    = "6"
    source      = var.admin_cidr

    tcp_options {
      min = 9090
      max = 9090
    }
  }

  ingress_security_rules {
    description = "Alertmanager from admin network"
    protocol    = "6"
    source      = var.admin_cidr

    tcp_options {
      min = 9093
      max = 9093
    }
  }

  ingress_security_rules {
    description = "Grafana from admin network"
    protocol    = "6"
    source      = var.admin_cidr

    tcp_options {
      min = 3000
      max = 3000
    }
  }

  ingress_security_rules {
    description = "Private VCN traffic to control-plane services"
    protocol    = "6"
    source      = var.vcn_cidr

    tcp_options {
      min = 8080
      max = 9108
    }
  }
}

resource "oci_core_security_list" "private" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.forge.id
  display_name   = "forge-private-security"
  freeform_tags  = local.common_tags

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  ingress_security_rules {
    description = "SSH and Forge services from VCN"
    protocol    = "6"
    source      = var.vcn_cidr

    tcp_options {
      min = 22
      max = 22
    }
  }

  ingress_security_rules {
    description = "Worker exporter from VCN"
    protocol    = "6"
    source      = var.vcn_cidr

    tcp_options {
      min = 9108
      max = 9108
    }
  }

  ingress_security_rules {
    description = "Application ports from Caddy/control plane inside VCN"
    protocol    = "6"
    source      = var.vcn_cidr

    tcp_options {
      min = 1024
      max = 65535
    }
  }
}

resource "oci_core_subnet" "public" {
  compartment_id             = var.compartment_ocid
  vcn_id                     = oci_core_vcn.forge.id
  cidr_block                 = var.public_subnet_cidr
  display_name               = "forge-public-subnet"
  dns_label                  = "public"
  route_table_id             = oci_core_route_table.public.id
  security_list_ids          = [oci_core_security_list.public.id]
  prohibit_public_ip_on_vnic = false
  freeform_tags              = local.common_tags
}

resource "oci_core_subnet" "private" {
  compartment_id             = var.compartment_ocid
  vcn_id                     = oci_core_vcn.forge.id
  cidr_block                 = var.private_subnet_cidr
  display_name               = "forge-private-subnet"
  dns_label                  = "private"
  route_table_id             = var.worker_assign_public_ip ? oci_core_route_table.public.id : oci_core_route_table.private[0].id
  security_list_ids          = [oci_core_security_list.private.id]
  prohibit_public_ip_on_vnic = !var.worker_assign_public_ip
  freeform_tags              = local.common_tags
}

resource "oci_core_instance" "control_plane" {
  availability_domain = local.availability_domain
  compartment_id      = var.compartment_ocid
  display_name        = "forge-control-plane"
  shape               = var.control_plane_shape
  freeform_tags       = merge(local.common_tags, { Role = "control-plane" })

  dynamic "shape_config" {
    for_each = var.control_plane_use_flex_shape_config ? [1] : []
    content {
      ocpus         = var.control_plane_ocpus
      memory_in_gbs = var.control_plane_memory_gbs
    }
  }

  create_vnic_details {
    subnet_id        = oci_core_subnet.public.id
    assign_public_ip = true
    hostname_label   = "control"
  }

  source_details {
    source_type             = "image"
    source_id               = data.oci_core_images.control_plane.images[0].id
    boot_volume_size_in_gbs = var.boot_volume_size_gbs
  }

  metadata = {
    ssh_authorized_keys = file(var.ssh_public_key_path)
  }
}

resource "oci_core_instance" "worker" {
  availability_domain = local.availability_domain
  compartment_id      = var.compartment_ocid
  display_name        = "forge-worker-1"
  shape               = var.worker_shape
  freeform_tags       = merge(local.common_tags, { Role = "worker" })

  dynamic "shape_config" {
    for_each = var.worker_use_flex_shape_config ? [1] : []
    content {
      ocpus         = var.worker_ocpus
      memory_in_gbs = var.worker_memory_gbs
    }
  }

  create_vnic_details {
    subnet_id        = oci_core_subnet.private.id
    assign_public_ip = var.worker_assign_public_ip
    hostname_label   = "worker1"
  }

  source_details {
    source_type             = "image"
    source_id               = data.oci_core_images.ubuntu_a1.images[0].id
    boot_volume_size_in_gbs = var.boot_volume_size_gbs
  }

  metadata = {
    ssh_authorized_keys = file(var.ssh_public_key_path)
  }
}

resource "oci_dns_zone" "forge" {
  count          = var.create_dns_zone ? 1 : 0
  compartment_id = var.compartment_ocid
  name           = var.base_domain
  zone_type      = "PRIMARY"
  scope          = "GLOBAL"
  freeform_tags  = local.common_tags
}

resource "oci_dns_rrset" "root_a" {
  count           = var.manage_dns_records && (var.create_dns_zone || var.existing_dns_zone_id != "") ? 1 : 0
  zone_name_or_id = var.create_dns_zone ? oci_dns_zone.forge[0].id : var.existing_dns_zone_id
  domain          = var.base_domain
  rtype           = "A"
  scope           = "GLOBAL"

  items {
    domain = var.base_domain
    rtype  = "A"
    rdata  = oci_core_instance.control_plane.public_ip
    ttl    = 300
  }
}

resource "oci_dns_rrset" "wildcard_a" {
  count           = var.manage_dns_records && (var.create_dns_zone || var.existing_dns_zone_id != "") ? 1 : 0
  zone_name_or_id = var.create_dns_zone ? oci_dns_zone.forge[0].id : var.existing_dns_zone_id
  domain          = "*.${var.base_domain}"
  rtype           = "A"
  scope           = "GLOBAL"

  items {
    domain = "*.${var.base_domain}"
    rtype  = "A"
    rdata  = oci_core_instance.control_plane.public_ip
    ttl    = 300
  }
}

resource "local_file" "ansible_inventory" {
  filename = "${path.module}/../ansible/inventory.ini"
  content = templatefile("${path.module}/inventory.tpl", {
    control_plane_public_ip  = oci_core_instance.control_plane.public_ip
    control_plane_private_ip = oci_core_instance.control_plane.private_ip
    worker_private_ip        = oci_core_instance.worker.private_ip
    base_domain              = var.base_domain
  })
}
