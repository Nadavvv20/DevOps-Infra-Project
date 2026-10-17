variable "aws_region" {
  type    = string
  default = "il-central-1"
}

variable "project_name" {
  type    = string
  default = "online-boutique"
}

variable "vpc_cidr" {
  type    = string
  default = "10.1.0.0/16"
}

variable "cluster_version" {
  type    = string
  default = "1.37"
}

variable "enable_ecr" {
  type        = bool
  default     = false
  description = "Set to true to provision private AWS ECR repositories for all 11 microservices (Option B)"
}

variable "access_entries" {
  type        = any
  default     = {}
  description = "Map of access entries to grant cluster access to IAM principals (e.g. AWS Console users)"
}

variable "secret_recovery_window" {
  type        = number
  default     = 30
  description = "Days before Secrets Manager permanently deletes a secret. Default 30 for prod."
}

