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

variable "order_events_queue_arn" {
  type        = string
  description = "ARN of the primary SQS order-events queue for IAM policies."
}

variable "order_events_dlq_arn" {
  type        = string
  description = "ARN of the SQS dead-letter queue for IAM policies."
}

variable "tags" {
  type    = map(string)
  default = {}
}
