# Two regions through a provider alias whose region comes from a variable,
# plus a burstable instance in unlimited CPU-credit mode and a classic ELB.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.68.0"
    }
  }
}

variable "dr_region" {
  type    = string
  default = "eu-west-1"
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "mock_access_key"
  secret_key                  = "mock_secret_key"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

provider "aws" {
  alias                       = "dr"
  region                      = var.dr_region
  access_key                  = "mock_access_key"
  secret_key                  = "mock_secret_key"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

resource "aws_instance" "primary" {
  ami           = "ami-0c02fb55956c7d316"
  instance_type = "t3.large"

  credit_specification {
    cpu_credits = "unlimited"
  }

  root_block_device {
    volume_type = "gp2"
    volume_size = 40
  }

  ebs_block_device {
    device_name = "/dev/sdf"
    volume_type = "st1"
    volume_size = 500
  }
}

resource "aws_instance" "standby" {
  provider      = aws.dr
  ami           = "ami-0d71ea30463e0ff8d"
  instance_type = "t3.large"

  root_block_device {
    volume_type = "gp3"
    volume_size = 40
  }
}

resource "aws_db_instance" "replica" {
  provider                    = aws.dr
  identifier                  = "orders-dr"
  engine                      = "mysql"
  engine_version              = "8.4.6"
  instance_class              = "db.r6g.large"
  storage_type                = "io1"
  allocated_storage           = 200
  iops                        = 3000
  username                    = "app"
  manage_master_user_password = true
  skip_final_snapshot         = true
}

resource "aws_s3_bucket" "archive" {
  provider = aws.dr
  bucket   = "orders-archive-example"
}

resource "aws_elb" "legacy" {
  name               = "legacy-classic"
  availability_zones = ["us-east-1a", "us-east-1b"]

  listener {
    instance_port     = 80
    instance_protocol = "http"
    lb_port           = 80
    lb_protocol       = "http"
  }
}
