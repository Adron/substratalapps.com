terraform {
  required_version = ">= 1.10"
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
  # bucket is passed at init: -backend-config="bucket=<TF_STATE_BUCKET>".
  backend "s3" {
    key          = "prod/terraform.tfstate"
    region       = "us-east-1"
    use_lockfile = true # native S3 locking; no DynamoDB table
    encrypt      = true
  }
}

provider "aws" {
  region = var.region
  default_tags { tags = { project = "substratal", managed_by = "terraform", stack = "prod" } }
}
