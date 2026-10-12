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

variable "db_name" {
  type    = string
  default = "onlineboutique"
}

variable "db_master_username" {
  type    = string
  default = "boutique_admin"
}

variable "engine_version" {
  type    = string
  default = "15.4"
}

variable "instance_class" {
  type    = string
  default = "db.t4g.medium"
}

variable "instance_count" {
  type    = number
  default = 2
  description = "Writer + Readers. Set 1 for dev."
}

variable "backup_retention_period" {
  type    = number
  default = 1
}

variable "deletion_protection" {
  type    = bool
  default = true
}

variable "skip_final_snapshot" {
  type    = bool
  default = false
}

variable "secret_recovery_window" {
  type    = number
  default = 7
}

variable "tags" {
  type    = map(string)
  default = {}
}
