data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_ami" "ubuntu_jammy_x86" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_ami" "ubuntu_jammy_arm64" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-arm64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

locals {
  control_plane_arch = startswith(var.control_plane_instance_type, "t4g.") ? "arm64" : "x86_64"
  worker_arch        = startswith(var.worker_instance_type, "t4g.") ? "arm64" : "x86_64"
  ssh_private_key    = var.ssh_private_key_path != "" ? var.ssh_private_key_path : trimsuffix(var.ssh_public_key_path, ".pub")

  common_tags = {
    Project = "Forge"
  }
}

resource "aws_vpc" "forge" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = merge(local.common_tags, {
    Name = "forge-vpc"
  })
}

resource "aws_internet_gateway" "forge" {
  vpc_id = aws_vpc.forge.id

  tags = merge(local.common_tags, {
    Name = "forge-internet-gateway"
  })
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.forge.id
  cidr_block              = var.public_subnet_cidr
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = merge(local.common_tags, {
    Name = "forge-public-subnet"
  })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.forge.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.forge.id
  }

  tags = merge(local.common_tags, {
    Name = "forge-public-routes"
  })
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

resource "aws_security_group" "control_plane" {
  name        = "forge-control-plane"
  description = "Forge control-plane ingress"
  vpc_id      = aws_vpc.forge.id

  ingress {
    description = "SSH from admin network"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.admin_cidr]
  }

  ingress {
    description = "HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "Control-plane API from Forge VPC"
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  ingress {
    description = "Prometheus from admin network"
    from_port   = 9090
    to_port     = 9090
    protocol    = "tcp"
    cidr_blocks = [var.admin_cidr]
  }

  ingress {
    description = "Alertmanager from admin network"
    from_port   = 9093
    to_port     = 9093
    protocol    = "tcp"
    cidr_blocks = [var.admin_cidr]
  }

  ingress {
    description = "Grafana from admin network"
    from_port   = 3000
    to_port     = 3000
    protocol    = "tcp"
    cidr_blocks = [var.admin_cidr]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "forge-control-plane"
  })
}

resource "aws_security_group" "worker" {
  name        = "forge-worker"
  description = "Forge worker ingress"
  vpc_id      = aws_vpc.forge.id

  ingress {
    description     = "SSH from control plane"
    from_port       = 22
    to_port         = 22
    protocol        = "tcp"
    security_groups = [aws_security_group.control_plane.id]
  }

  ingress {
    description     = "Worker exporter from control plane"
    from_port       = 9108
    to_port         = 9108
    protocol        = "tcp"
    security_groups = [aws_security_group.control_plane.id]
  }

  ingress {
    description     = "Application ports from Caddy/control plane"
    from_port       = 20000
    to_port         = 39999
    protocol        = "tcp"
    security_groups = [aws_security_group.control_plane.id]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "forge-worker"
  })
}

resource "aws_key_pair" "forge" {
  key_name   = var.key_pair_name
  public_key = file(var.ssh_public_key_path)

  tags = local.common_tags
}

resource "aws_instance" "control_plane" {
  ami                         = local.control_plane_arch == "arm64" ? data.aws_ami.ubuntu_jammy_arm64.id : data.aws_ami.ubuntu_jammy_x86.id
  instance_type               = var.control_plane_instance_type
  subnet_id                   = aws_subnet.public.id
  vpc_security_group_ids      = [aws_security_group.control_plane.id]
  key_name                    = aws_key_pair.forge.key_name
  associate_public_ip_address = true

  root_block_device {
    volume_size = var.root_volume_size_gb
    volume_type = "gp3"
    encrypted   = true
  }

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "disabled"
  }

  tags = merge(local.common_tags, {
    Name = "forge-control-plane"
    Role = "control-plane"
  })
}

resource "aws_instance" "worker" {
  ami                         = local.worker_arch == "arm64" ? data.aws_ami.ubuntu_jammy_arm64.id : data.aws_ami.ubuntu_jammy_x86.id
  instance_type               = var.worker_instance_type
  subnet_id                   = aws_subnet.public.id
  vpc_security_group_ids      = [aws_security_group.worker.id]
  key_name                    = aws_key_pair.forge.key_name
  associate_public_ip_address = true

  root_block_device {
    volume_size = var.root_volume_size_gb
    volume_type = "gp3"
    encrypted   = true
  }

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "disabled"
  }

  tags = merge(local.common_tags, {
    Name = "forge-worker-1"
    Role = "worker"
  })
}

resource "aws_route53_record" "root_a" {
  count   = var.manage_route53 && var.route53_zone_id != "" ? 1 : 0
  zone_id = var.route53_zone_id
  name    = var.base_domain
  type    = "A"
  ttl     = 300
  records = [aws_instance.control_plane.public_ip]
}

resource "aws_route53_record" "wildcard_a" {
  count   = var.manage_route53 && var.route53_zone_id != "" ? 1 : 0
  zone_id = var.route53_zone_id
  name    = "*.${var.base_domain}"
  type    = "A"
  ttl     = 300
  records = [aws_instance.control_plane.public_ip]
}

resource "local_file" "ansible_inventory" {
  filename = "${path.module}/../../ansible/inventory.ini"
  content = templatefile("${path.module}/inventory.tpl", {
    control_plane_public_ip  = aws_instance.control_plane.public_ip
    control_plane_private_ip = aws_instance.control_plane.private_ip
    worker_public_ip         = aws_instance.worker.public_ip
    worker_private_ip        = aws_instance.worker.private_ip
    base_domain              = var.base_domain
    ssh_private_key_path     = local.ssh_private_key
  })
  file_permission = "0644"
}
