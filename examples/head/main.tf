# "head": the same stack as ../base after a hypothetical pull request that
# scales the web tier, makes the database Multi-AZ on bigger gp3 storage,
# moves Lambda to Graviton, migrates the scratch volume to gp3, and adds an
# io2 journal volume, a Network Load Balancer and two worker functions.

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
  instance_type      = "m6i.large"
  instance_count     = 3
  root_volume_gb     = 50
  public_subnet_ids  = aws_subnet.public[*].id
  private_subnet_ids = aws_subnet.private[*].id
  nat_gateway_count  = 2
}

module "data" {
  source            = "../modules/data-tier"
  name              = "shop"
  db_instance_class = "db.m6g.large"
  db_multi_az       = true
  db_storage_type   = "gp3"
  db_storage_gb     = 500
  db_iops           = 15000
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
  memory_size   = 1024
  architectures = ["arm64"]
}

resource "aws_lambda_function" "workers" {
  for_each      = toset(["ocr", "resize"])
  function_name = "worker-${each.key}"
  role          = aws_iam_role.lambda.arn
  runtime       = "python3.12"
  handler       = "app.handler"
  s3_bucket     = "shop-artifacts-example"
  s3_key        = "worker-${each.key}.zip"
  memory_size   = 2048
  architectures = ["arm64"]

  ephemeral_storage {
    size = 2048
  }
}

resource "aws_ebs_volume" "scratch" {
  availability_zone = "us-east-1a"
  type              = "gp3"
  size              = 200
  iops              = 4000
  throughput        = 250
}

resource "aws_ebs_volume" "journal" {
  availability_zone = "us-east-1a"
  type              = "io2"
  size              = 500
  iops              = 40000
}

resource "aws_lb" "ingest" {
  name               = "shop-ingest-nlb"
  load_balancer_type = "network"
  subnets            = aws_subnet.public[*].id
}
