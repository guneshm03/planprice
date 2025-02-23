# Web tier: an Application Load Balancer in front of N EC2 instances, with
# one NAT gateway per private subnet for outbound traffic.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

variable "name" {
  type = string
}

variable "ami" {
  type        = string
  description = "AMI id. Hard-coded in the examples so `plan` needs no API calls."
}

variable "instance_type" {
  type = string
}

variable "instance_count" {
  type = number
}

variable "root_volume_gb" {
  type = number
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "nat_gateway_count" {
  type = number
}

resource "aws_instance" "web" {
  count         = var.instance_count
  ami           = var.ami
  instance_type = var.instance_type
  subnet_id     = var.private_subnet_ids[count.index % length(var.private_subnet_ids)]

  root_block_device {
    volume_type = "gp3"
    volume_size = var.root_volume_gb
  }

  tags = {
    Name = "${var.name}-web-${count.index}"
  }
}

resource "aws_lb" "app" {
  name               = "${var.name}-alb"
  load_balancer_type = "application"
  subnets            = var.public_subnet_ids
}

resource "aws_lb_target_group" "web" {
  name     = "${var.name}-web"
  port     = 80
  protocol = "HTTP"
  vpc_id   = "vpc-0a1b2c3d4e5f60718"
}

resource "aws_eip" "nat" {
  count  = var.nat_gateway_count
  domain = "vpc"
}

resource "aws_nat_gateway" "egress" {
  count         = var.nat_gateway_count
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = var.public_subnet_ids[count.index]
}
