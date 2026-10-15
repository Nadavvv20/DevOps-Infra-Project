output "order_events_topic_arn" {
  description = "ARN of the primary order-events SNS topic."
  value       = aws_sns_topic.order_events.arn
}

output "email_queue_url" {
  description = "URL of the order-events-email SQS queue."
  value       = aws_sqs_queue.order_events_email.url
}

output "email_queue_arn" {
  description = "ARN of the order-events-email SQS queue."
  value       = aws_sqs_queue.order_events_email.arn
}

output "email_dlq_url" {
  description = "URL of the order-events-email dead-letter queue."
  value       = aws_sqs_queue.order_events_email_dlq.url
}

output "email_dlq_arn" {
  description = "ARN of the order-events-email dead-letter queue."
  value       = aws_sqs_queue.order_events_email_dlq.arn
}

output "shipping_queue_url" {
  description = "URL of the order-events-shipping SQS queue."
  value       = aws_sqs_queue.order_events_shipping.url
}

output "shipping_queue_arn" {
  description = "ARN of the order-events-shipping SQS queue."
  value       = aws_sqs_queue.order_events_shipping.arn
}

output "shipping_dlq_url" {
  description = "URL of the order-events-shipping dead-letter queue."
  value       = aws_sqs_queue.order_events_shipping_dlq.url
}

output "shipping_dlq_arn" {
  description = "ARN of the order-events-shipping dead-letter queue."
  value       = aws_sqs_queue.order_events_shipping_dlq.arn
}

output "secret_arn" {
  description = "ARN of the Secrets Manager secret holding event bus config for ExternalSecrets."
  value       = aws_secretsmanager_secret.sqs_config.arn
}
