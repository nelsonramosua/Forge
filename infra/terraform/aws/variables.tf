variable "aws_region" {
  description = "AWS region for Forge. eu-west-1 is a good default for Portugal."
  type        = string
  default     = "eu-west-1"
}

variable "base_domain" {
  description = "Public domain used by Forge, for example forge.example.com."
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

variable "ssh_public_key_path" {
  description = "Path to the public SSH key imported as the EC2 key pair."
  type        = string
}

variable "ssh_private_key_path" {
  description = "Path to the private SSH key used by generated Ansible ProxyJump config."
  type        = string
  default     = ""
}

variable "key_pair_name" {
  description = "Name of the EC2 key pair managed by Terraform."
  type        = string
  default     = "forge-aws"
}

variable "vpc_cidr" {
  description = "CIDR for the Forge VPC."
  type        = string
  default     = "10.52.0.0/16"
}

variable "public_subnet_cidr" {
  description = "CIDR for the public subnet."
  type        = string
  default     = "10.52.1.0/24"
}

variable "private_subnet_cidr" {
  description = "CIDR for the private worker subnet."
  type        = string
  default     = "10.52.2.0/24"
}

variable "create_nat_gateway" {
  description = "Create a NAT gateway for private worker outbound access. Leave true for the standard private-worker layout; when false, the worker falls back to the public subnet for outbound package access."
  type        = bool
  default     = true
}

variable "control_plane_instance_type" {
  description = "EC2 instance type for the control plane. t3.micro is broadly Free Tier eligible depending on account terms."
  type        = string
  default     = "t3.micro"
}

variable "worker_instance_type" {
  description = "EC2 instance type for the worker. t3.micro is safest for classic Free Tier; t3.small/t4g.small may be eligible for newer accounts."
  type        = string
  default     = "t3.micro"
}

variable "worker_assign_public_ip" {
  description = "Legacy compatibility switch for public-worker setups. Private-worker deployments ignore this and keep the worker on a private subnet."
  type        = bool
  default     = false
}

variable "root_volume_size_gb" {
  description = "Root EBS volume size for each instance."
  type        = number
  default     = 30
}

variable "manage_route53" {
  description = "Create Route 53 A records for base_domain and wildcard. Hosted zones can incur a monthly charge, so this defaults to false."
  type        = bool
  default     = false
}

variable "route53_zone_id" {
  description = "Existing Route 53 hosted zone ID used when manage_route53 is true."
  type        = string
  default     = ""
}
