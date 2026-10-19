variable "cluster_name" {
  type = string
}

variable "oidc_provider_arn" {
  type        = string
  description = "ARN of the EKS OIDC provider used to establish trust for IRSA."
}

variable "oidc_issuer_url" {
  type        = string
  description = "OIDC issuer URL (without https://) used in IAM trust policy conditions. Example: oidc.eks.il-central-1.amazonaws.com/id/XXXXXXXX"
}

variable "secrets_manager_arns" {
  type        = list(string)
  description = "ARNs of Secrets Manager secrets External Secrets Operator is allowed to read."
}

variable "order_events_topic_arn" {
  type        = string
  description = "ARN of the SNS order-events topic for checkoutservice publishing."
}

variable "email_queue_arn" {
  type        = string
  description = "ARN of the email SQS queue for emailservice IAM policy."
}

variable "email_dlq_arn" {
  type        = string
  description = "ARN of the email SQS dead-letter queue."
}

variable "shipping_queue_arn" {
  type        = string
  description = "ARN of the shipping SQS queue for shippingservice IAM policy."
}

variable "shipping_dlq_arn" {
  type        = string
  description = "ARN of the shipping SQS dead-letter queue."
}

variable "ads_bucket_arn" {
  type        = string
  default     = ""
  description = "ARN of the S3 ads media bucket for adservice read access."
}

variable "tags" {
  type    = map(string)
  default = {}
}
