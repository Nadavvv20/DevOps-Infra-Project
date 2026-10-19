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

output "order_events_topic_arn" {
  description = "ARN of the primary order-events SNS topic."
  value       = module.sqs.order_events_topic_arn
}

output "email_queue_url" {
  description = "URL of the order-events-email SQS queue."
  value       = module.sqs.email_queue_url
}

output "shipping_queue_url" {
  description = "URL of the order-events-shipping SQS queue."
  value       = module.sqs.shipping_queue_url
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

output "aurora_endpoint" {
  description = "Aurora / RDS PostgreSQL cluster endpoint"
  value       = module.rds_aurora.cluster_endpoint
}

output "ads_s3_bucket_name" {
  description = "Name of the S3 bucket storing ad banners and catalog"
  value       = module.ads_cdn.bucket_name
}

output "ads_cloudfront_domain_name" {
  description = "CloudFront CDN domain name for ads"
  value       = module.ads_cdn.cloudfront_domain_name
}

output "ads_cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = module.ads_cdn.cloudfront_distribution_id
}

output "adservice_role_arn" {
  description = "IRSA role ARN for adservice S3 read access"
  value       = module.iam_roles.adservice_role_arn
}

