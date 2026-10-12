output "external_secrets_role_arn" {
  value = module.external_secrets_irsa.iam_role_arn
}

output "alb_controller_role_arn" {
  value = module.alb_controller_irsa.iam_role_arn
}

output "cloudwatch_agent_role_arn" {
  value = module.cloudwatch_agent_irsa.iam_role_arn
}
