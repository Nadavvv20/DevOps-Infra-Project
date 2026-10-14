variable "cluster_name" {
  type        = string
  description = "Cluster name prefix for all secret names."
}

variable "secret_recovery_window" {
  type        = number
  default     = 7
  description = "Days before Secrets Manager permanently deletes a secret. Use 0 for dev."
}

variable "tags" {
  type    = map(string)
  default = {}
}
