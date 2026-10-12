variable "cluster_name"              { type = string }
variable "cluster_endpoint"          { type = string }
variable "cluster_ca_data"           { type = string }
variable "vpc_id"                    { type = string  default = "" }
variable "aws_region"                { type = string }
variable "karpenter_irsa_arn"        { type = string }
variable "karpenter_node_role_arn"   { type = string }
variable "interruption_queue_name"   { type = string }
variable "alb_controller_role_arn"   { type = string }
variable "external_secrets_role_arn" { type = string }
variable "cloudwatch_agent_role_arn" { type = string }
variable "aurora_secret_arn"         { type = string }
variable "redis_secret_arn"          { type = string }

variable "karpenter_version"          { type = string  default = "1.0.1" }
variable "alb_controller_version"     { type = string  default = "1.8.1" }
variable "external_secrets_version"   { type = string  default = "0.10.3" }
variable "argocd_version"             { type = string  default = "7.4.3" }
variable "prometheus_stack_version"   { type = string  default = "61.9.0" }

variable "argocd_hostname"            { type = string  default = "argocd.example.com" }
variable "grafana_hostname"           { type = string  default = "grafana.example.com" }
variable "grafana_admin_password"     { type = string  default = "ChangeMe!123"  sensitive = true }
variable "acm_certificate_arn"        { type = string  default = "" }

variable "tags" {
  type    = map(string)
  default = {}
}
