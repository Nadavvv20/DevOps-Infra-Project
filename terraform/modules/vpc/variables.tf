variable "cluster_name" {
  type = string
}

variable "vpc_cidr" {
  type    = string
  default = "10.0.0.0/16"
}

variable "single_nat_gateway" {
  type        = bool
  default     = false
  description = "Use a single NAT Gateway. Set true for dev to reduce cost."
}

variable "tags" {
  type    = map(string)
  default = {}
}
