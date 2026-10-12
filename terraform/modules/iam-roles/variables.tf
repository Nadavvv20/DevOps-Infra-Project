variable "cluster_name" {
  type = string
}

variable "oidc_provider_arn" {
  type = string
}

variable "secrets_manager_arns" {
  type        = list(string)
  description = "ARNs of Secrets Manager secrets External Secrets Operator is allowed to read."
}

variable "tags" {
  type    = map(string)
  default = {}
}
