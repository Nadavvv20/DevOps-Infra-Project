output "external_secrets_role_arn" {
  value = module.external_secrets_irsa.iam_role_arn
}

output "alb_controller_role_arn" {
  value = module.alb_controller_irsa.iam_role_arn
}

output "cloudwatch_agent_role_arn" {
  value = module.cloudwatch_agent_irsa.iam_role_arn
}

output "checkoutservice_role_arn" {
  description = "IRSA role ARN for checkoutservice (SQS producer)."
  value       = aws_iam_role.checkoutservice_sqs.arn
}

output "emailservice_role_arn" {
  description = "IRSA role ARN for emailservice (SQS consumer)."
  value       = aws_iam_role.emailservice_sqs.arn
}

output "shippingservice_role_arn" {
  description = "IRSA role ARN for shippingservice (SQS consumer)."
  value       = aws_iam_role.shippingservice_sqs.arn
}
