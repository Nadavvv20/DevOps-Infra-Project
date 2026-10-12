terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.12"
    }
    kubectl = {
      source  = "gavinbunney/kubectl"
      version = "~> 1.14"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  backend "s3" {
    bucket         = "mycompany-online-boutique-tfstate"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
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

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)

    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", module.eks.cluster_name]
    }
  }
}

provider "kubectl" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  load_config_file       = false

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", module.eks.cluster_name]
  }
}

locals {
  env          = "prod"
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
  single_nat_gateway = false
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
  instance_class             = "db.r7g.large"
  instance_count             = 2
  deletion_protection        = true
  skip_final_snapshot        = false
  tags                       = local.common_tags
}

module "elasticache" {
  source = "../../modules/elasticache"

  cluster_name               = local.cluster_name
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id
  node_type                  = "cache.r7g.large"
  num_cache_clusters         = 2
  tags                       = local.common_tags
}

module "ecr" {
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

module "helm_releases" {
  source = "../../modules/helm-releases"

  cluster_name              = local.cluster_name
  cluster_endpoint          = module.eks.cluster_endpoint
  cluster_ca_data           = module.eks.cluster_certificate_authority_data
  karpenter_irsa_arn        = module.karpenter.irsa_arn
  karpenter_node_role_arn   = module.karpenter.node_role_arn
  interruption_queue_name   = module.karpenter.interruption_queue_name
  alb_controller_role_arn   = module.iam_roles.alb_controller_role_arn
  external_secrets_role_arn = module.iam_roles.external_secrets_role_arn
  cloudwatch_agent_role_arn = module.iam_roles.cloudwatch_agent_role_arn
  aurora_secret_arn         = module.rds_aurora.secret_arn
  redis_secret_arn          = module.elasticache.secret_arn
  aws_region                = var.aws_region
  tags                      = local.common_tags
}
