aws_region      = "il-central-1"
project_name    = "online-boutique"
vpc_cidr        = "10.0.0.0/16"
cluster_version = "1.37"
access_entries = {
  console_user = {
    principal_arn = "arn:aws:iam::965085140318:root"
    policy_associations = {
      admin = {
        policy_arn = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
        access_scope = {
          type = "cluster"
        }
      }
    }
  }
  github_actions_deploy = {
    principal_arn = "arn:aws:iam::965085140318:role/AWS_DEPLOY_ROLE_ARN"
    policy_associations = {
      admin = {
        policy_arn = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
        access_scope = {
          type = "cluster"
        }
      }
    }
  }
}

enable_ecr = false

