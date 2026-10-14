output "order_events_queue_url" {
  description = "URL of the primary order-events SQS queue."
  value       = aws_sqs_queue.order_events.url
}

output "order_events_queue_arn" {
  description = "ARN of the primary order-events SQS queue."
  value       = aws_sqs_queue.order_events.arn
}

output "order_events_dlq_url" {
  description = "URL of the dead-letter queue."
  value       = aws_sqs_queue.order_events_dlq.url
}

output "order_events_dlq_arn" {
  description = "ARN of the dead-letter queue."
  value       = aws_sqs_queue.order_events_dlq.arn
}

output "secret_arn" {
  description = "ARN of the Secrets Manager secret holding queue config for ExternalSecrets."
  value       = aws_secretsmanager_secret.sqs_config.arn
}
