# "base": the configuration on the main branch. Planned against a mock AWS
# provider (static fake credentials, all account/credential checks skipped),
# so `terraform plan` makes no AWS API calls. See scripts/gen-plans.sh.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.68.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "mock_access_key"
  secret_key                  = "mock_secret_key"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "public" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.0.${count.index}.0/24"
  availability_zone = ["us-east-1a", "us-east-1b"][count.index]
}

resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.0.${count.index + 10}.0/24"
  availability_zone = ["us-east-1a", "us-east-1b"][count.index]
}

module "web" {
  source             = "../modules/web-tier"
  name               = "shop"
  ami                = "ami-0c02fb55956c7d316"
  instance_type      = "t3.medium"
  instance_count     = 2
  root_volume_gb     = 30
  public_subnet_ids  = aws_subnet.public[*].id
  private_subnet_ids = aws_subnet.private[*].id
  nat_gateway_count  = 1
}

module "data" {
  source            = "../modules/data-tier"
  name              = "shop"
  db_instance_class = "db.t3.medium"
  db_multi_az       = false
  db_storage_type   = "gp3"
  db_storage_gb     = 100
}

resource "aws_iam_role" "lambda" {
  name = "thumbnailer"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
}

resource "aws_lambda_function" "thumbnailer" {
  function_name = "thumbnailer"
  role          = aws_iam_role.lambda.arn
  runtime       = "python3.12"
  handler       = "app.handler"
  s3_bucket     = "shop-artifacts-example"
  s3_key        = "thumbnailer.zip"
  memory_size   = 512
  architectures = ["x86_64"]
}

resource "aws_ebs_volume" "scratch" {
  availability_zone = "us-east-1a"
  type              = "gp2"
  size              = 200
}
