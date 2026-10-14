variable "cluster_name" {
  type        = string
  description = "Unique cluster identifier used as a prefix for all resource names."
}

variable "secret_recovery_window" {
  type        = number
  default     = 7
  description = "Number of days Secrets Manager waits before deleting a secret after deletion request. Set 0 for dev to allow immediate re-creation."
}

variable "tags" {
  type    = map(string)
  default = {}
}
