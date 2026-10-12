variable "cluster_name" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "eks_node_security_group_id" {
  type = string
}

variable "node_type" {
  type    = string
  default = "cache.t4g.small"
}

variable "num_cache_clusters" {
  type    = number
  default = 2
  description = "Set 1 for dev, 2+ for prod (enables Multi-AZ)."
}

variable "engine_version" {
  type    = string
  default = "7.1"
}

variable "secret_recovery_window" {
  type    = number
  default = 7
}

variable "tags" {
  type    = map(string)
  default = {}
}
