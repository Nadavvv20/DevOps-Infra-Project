aws_region      = "il-central-1"
project_name    = "online-boutique"
vpc_cidr        = "10.0.0.0/16"
cluster_version = "1.30"
access_entries = {
  console_user = {
    principal_arn = "arn:aws:iam::965085140318:user/nadavvv58"
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

