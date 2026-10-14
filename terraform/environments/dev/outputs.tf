output "cluster_name" {
  description = "EKS cluster name"
  value       = module.eks.cluster_name
}

output "cluster_endpoint" {
  description = "EKS cluster endpoint"
  value       = module.eks.cluster_endpoint
}

output "vpc_id" {
  description = "VPC ID"
  value       = module.vpc.vpc_id
}

output "ecr_repository_urls" {
  description = "ECR repository URLs for microservices"
  value       = var.enable_ecr ? module.ecr[0].repository_urls : {}
}

output "order_events_queue_url" {
  description = "URL of the primary SQS order-events queue."
  value       = module.sqs.order_events_queue_url
}

output "checkoutservice_role_arn" {
  description = "IRSA role ARN to annotate the checkoutservice Kubernetes ServiceAccount."
  value       = module.iam_roles.checkoutservice_role_arn
}

output "emailservice_role_arn" {
  description = "IRSA role ARN to annotate the emailservice Kubernetes ServiceAccount."
  value       = module.iam_roles.emailservice_role_arn
}

output "shippingservice_role_arn" {
  description = "IRSA role ARN to annotate the shippingservice Kubernetes ServiceAccount."
  value       = module.iam_roles.shippingservice_role_arn
}
