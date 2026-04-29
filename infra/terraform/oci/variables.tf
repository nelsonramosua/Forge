variable "compartment_ocid" {
  description = "OCI compartment OCID where Forge resources will be created. Can be supplied with TF_VAR_compartment_ocid."
  type        = string
}

variable "region" {
  description = "OCI region identifier, for example eu-madrid-1."
  type        = string
}

variable "availability_domain" {
  description = "Optional availability domain name. Leave empty to use the first AD returned by OCI."
  type        = string
  default     = ""
}

variable "control_plane_shape" {
  description = "OCI shape for the control-plane instance. Use VM.Standard.A1.Flex for Always Free Ampere A1."
  type        = string
  default     = "VM.Standard.A1.Flex"
}

variable "control_plane_use_flex_shape_config" {
  description = "Whether to set shape_config for the control-plane shape. Set false for fixed shapes such as VM.Standard.E2.1.Micro."
  type        = bool
  default     = true
}

variable "control_plane_ocpus" {
  description = "Control-plane OCPUs when using a flexible shape."
  type        = number
  default     = 1
}

variable "control_plane_memory_gbs" {
  description = "Control-plane memory in GB when using a flexible shape."
  type        = number
  default     = 6
}

variable "worker_shape" {
  description = "OCI shape for the worker instance. Use VM.Standard.A1.Flex for Always Free Ampere A1."
  type        = string
  default     = "VM.Standard.A1.Flex"
}

variable "worker_use_flex_shape_config" {
  description = "Whether to set shape_config for the worker shape."
  type        = bool
  default     = true
}

variable "worker_ocpus" {
  description = "Worker OCPUs when using a flexible shape."
  type        = number
  default     = 3
}

variable "worker_memory_gbs" {
  description = "Worker memory in GB when using a flexible shape."
  type        = number
  default     = 18
}

variable "base_domain" {
  description = "Public domain used by Forge, for example forge.example.com."
  type        = string
}

variable "ssh_public_key_path" {
  description = "Path to the public SSH key installed on both instances."
  type        = string
}

variable "admin_cidr" {
  description = "CIDR allowed to SSH and reach admin UIs such as Prometheus and Grafana."
  type        = string

  validation {
    condition     = can(cidrnetmask(var.admin_cidr)) && var.admin_cidr != "0.0.0.0/0"
    error_message = "admin_cidr must be a valid IPv4 CIDR and must not be 0.0.0.0/0."
  }
}

variable "vcn_cidr" {
  description = "CIDR for the Forge VCN."
  type        = string
  default     = "10.42.0.0/16"
}

variable "public_subnet_cidr" {
  description = "CIDR for the public control-plane subnet."
  type        = string
  default     = "10.42.1.0/24"
}

variable "private_subnet_cidr" {
  description = "CIDR for the private worker subnet."
  type        = string
  default     = "10.42.2.0/24"
}

variable "create_nat_gateway" {
  description = "Create a NAT gateway for private worker outbound access. Free-tier accounts often hit NAT limits, so this defaults to false."
  type        = bool
  default     = false
}

variable "worker_assign_public_ip" {
  description = "Assign a public IP to the worker for outbound Internet without NAT. Inbound traffic is still restricted by security lists."
  type        = bool
  default     = true
}

variable "create_dns_zone" {
  description = "Create an OCI DNS zone for base_domain. Disable if your tenancy has no remaining global DNS zone quota."
  type        = bool
  default     = false
}

variable "existing_dns_zone_id" {
  description = "Existing OCI DNS zone OCID to use when create_dns_zone is false. Leave empty to skip DNS record management."
  type        = string
  default     = ""
}

variable "manage_dns_records" {
  description = "Manage A records for base_domain and wildcard in OCI DNS. Requires create_dns_zone=true or existing_dns_zone_id."
  type        = bool
  default     = false
}

variable "image_operating_system" {
  description = "OCI platform image operating system filter."
  type        = string
  default     = "Canonical Ubuntu"
}

variable "image_operating_system_version" {
  description = "OCI platform image operating system version filter."
  type        = string
  default     = "22.04"
}

variable "boot_volume_size_gbs" {
  description = "Boot volume size for each instance. 50 GB keeps the two-VM layout within OCI Always Free block volume limits."
  type        = number
  default     = 50
}
