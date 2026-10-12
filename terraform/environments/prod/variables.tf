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
  default = "1.30"
}

variable "enable_ecr" {
  type        = bool
  default     = false
  description = "Set to true to provision private AWS ECR repositories for all 11 microservices (Option B)"
}

