terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  backend "s3" {
    bucket         = "mycompany-online-boutique-tfstate"
    key            = "dev/terraform.tfstate"
    region         = "il-central-1"
    dynamodb_table = "terraform-state-lock"
    encrypt        = true
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = local.common_tags
  }
}

locals {
  env          = "dev"
  cluster_name = "${var.project_name}-${local.env}"

  common_tags = {
    Project     = var.project_name
    Environment = local.env
    ManagedBy   = "terraform"
    Owner       = "platform-team"
  }
}

module "vpc" {
  source = "../../modules/vpc"

  cluster_name       = local.cluster_name
  vpc_cidr           = var.vpc_cidr
  single_nat_gateway = true
  tags               = local.common_tags
}

module "eks" {
  source = "../../modules/eks"

  cluster_name       = local.cluster_name
  cluster_version    = var.cluster_version
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids
  intra_subnet_ids   = module.vpc.intra_subnet_ids
  tags               = local.common_tags
}

module "karpenter" {
  source = "../../modules/karpenter"

  cluster_name      = local.cluster_name
  oidc_provider_arn = module.eks.oidc_provider_arn
  tags              = local.common_tags
}

module "rds_aurora" {
  source = "../../modules/rds-aurora"

  cluster_name               = local.cluster_name
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id
  instance_class             = "db.t4g.medium"
  instance_count             = 1
  deletion_protection        = false
  skip_final_snapshot        = true
  secret_recovery_window     = 0
  tags                       = local.common_tags
}

module "elasticache" {
  source = "../../modules/elasticache"

  cluster_name               = local.cluster_name
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id
  node_type                  = "cache.t4g.small"
  num_cache_clusters         = 1
  tags                       = local.common_tags
}

module "ecr" {
  count  = var.enable_ecr ? 1 : 0
  source = "../../modules/ecr"

  project_name = var.project_name
  tags         = local.common_tags
}

module "iam_roles" {
  source = "../../modules/iam-roles"

  cluster_name      = local.cluster_name
  oidc_provider_arn = module.eks.oidc_provider_arn
  secrets_manager_arns = [
    module.rds_aurora.secret_arn,
    module.elasticache.secret_arn,
  ]
  tags = local.common_tags
}

