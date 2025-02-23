# Data tier: one PostgreSQL RDS instance plus an asset bucket and a log bucket.

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

variable "db_instance_class" {
  type = string
}

variable "db_multi_az" {
  type = bool
}

variable "db_storage_type" {
  type = string
}

variable "db_storage_gb" {
  type = number
}

variable "db_iops" {
  type    = number
  default = null
}

resource "aws_db_instance" "main" {
  identifier                  = "${var.name}-db"
  engine                      = "postgres"
  engine_version              = "17.5"
  instance_class              = var.db_instance_class
  multi_az                    = var.db_multi_az
  storage_type                = var.db_storage_type
  allocated_storage           = var.db_storage_gb
  iops                        = var.db_iops
  username                    = "app"
  manage_master_user_password = true
  skip_final_snapshot         = true
}

resource "aws_s3_bucket" "assets" {
  bucket = "${var.name}-assets-example"
}

resource "aws_s3_bucket" "logs" {
  bucket = "${var.name}-logs-example"
}
