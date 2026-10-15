# ─────────────────────────────────────────────────────────────────────────────
# SQS + SNS Module — Fan-Out Event Bus for Online Boutique
#
# Architecture:
#  1. SNS Topic:        order-events
#  2. Email Queue:      order-events-email        (subscribes to order-events SNS topic)
#     Email DLQ:        order-events-email-dlq    (maxReceiveCount = 3)
#  3. Shipping Queue:   order-events-shipping     (subscribes to order-events SNS topic)
#     Shipping DLQ:     order-events-shipping-dlq (maxReceiveCount = 3)
#
# Producer (checkoutservice) publishes to the SNS Topic once.
# SNS fans out the message to both email and shipping SQS queues in parallel.
# Each queue has its own independent DLQ and queue policy.
# ─────────────────────────────────────────────────────────────────────────────

# ── SNS Topic ─────────────────────────────────────────────────────────────────
resource "aws_sns_topic" "order_events" {
  name = "${var.cluster_name}-order-events"
  tags = var.tags
}

# ── Email Service Queues & Subscriptions ──────────────────────────────────────

resource "aws_sqs_queue" "order_events_email_dlq" {
  name                      = "${var.cluster_name}-order-events-email-dlq"
  message_retention_seconds = 1209600 # 14 days
  sqs_managed_sse_enabled   = true
  tags                      = var.tags
}

resource "aws_sqs_queue" "order_events_email" {
  name                       = "${var.cluster_name}-order-events-email"
  visibility_timeout_seconds = 30
  message_retention_seconds  = 345600 # 4 days
  sqs_managed_sse_enabled    = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.order_events_email_dlq.arn
    maxReceiveCount     = 3
  })

  tags = var.tags
}

resource "aws_sqs_queue_redrive_allow_policy" "order_events_email_dlq" {
  queue_url = aws_sqs_queue.order_events_email_dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.order_events_email.arn]
  })
}

resource "aws_sqs_queue_policy" "order_events_email" {
  queue_url = aws_sqs_queue.order_events_email.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowSNSOrderEventsPublish"
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = aws_sqs_queue.order_events_email.arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = aws_sns_topic.order_events.arn
          }
        }
      }
    ]
  })
}

resource "aws_sns_topic_subscription" "order_events_email" {
  topic_arn            = aws_sns_topic.order_events.arn
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.order_events_email.arn
  raw_message_delivery = true
}

# ── Shipping Service Queues & Subscriptions ───────────────────────────────────

resource "aws_sqs_queue" "order_events_shipping_dlq" {
  name                      = "${var.cluster_name}-order-events-shipping-dlq"
  message_retention_seconds = 1209600 # 14 days
  sqs_managed_sse_enabled   = true
  tags                      = var.tags
}

resource "aws_sqs_queue" "order_events_shipping" {
  name                       = "${var.cluster_name}-order-events-shipping"
  visibility_timeout_seconds = 30
  message_retention_seconds  = 345600 # 4 days
  sqs_managed_sse_enabled    = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.order_events_shipping_dlq.arn
    maxReceiveCount     = 3
  })

  tags = var.tags
}

resource "aws_sqs_queue_redrive_allow_policy" "order_events_shipping_dlq" {
  queue_url = aws_sqs_queue.order_events_shipping_dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.order_events_shipping.arn]
  })
}

resource "aws_sqs_queue_policy" "order_events_shipping" {
  queue_url = aws_sqs_queue.order_events_shipping.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowSNSOrderEventsPublish"
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = aws_sqs_queue.order_events_shipping.arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = aws_sns_topic.order_events.arn
          }
        }
      }
    ]
  })
}

resource "aws_sns_topic_subscription" "order_events_shipping" {
  topic_arn            = aws_sns_topic.order_events.arn
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.order_events_shipping.arn
  raw_message_delivery = true
}

# ── Secrets Manager Configuration ─────────────────────────────────────────────
# ExternalSecrets syncs these keys into Kubernetes Secrets for pods
resource "aws_secretsmanager_secret" "sqs_config" {
  name                    = "${var.cluster_name}/sqs/order-events"
  description             = "SNS topic ARN and SQS queue URLs/ARNs for order-events fan-out"
  recovery_window_in_days = var.secret_recovery_window
  tags                    = var.tags
}

resource "aws_secretsmanager_secret_version" "sqs_config" {
  secret_id = aws_secretsmanager_secret.sqs_config.id
  secret_string = jsonencode({
    order_events_topic_arn = aws_sns_topic.order_events.arn
    email_queue_url        = aws_sqs_queue.order_events_email.url
    email_queue_arn        = aws_sqs_queue.order_events_email.arn
    email_dlq_url          = aws_sqs_queue.order_events_email_dlq.url
    email_dlq_arn          = aws_sqs_queue.order_events_email_dlq.arn
    shipping_queue_url     = aws_sqs_queue.order_events_shipping.url
    shipping_queue_arn     = aws_sqs_queue.order_events_shipping.arn
    shipping_dlq_url       = aws_sqs_queue.order_events_shipping_dlq.url
    shipping_dlq_arn       = aws_sqs_queue.order_events_shipping_dlq.arn
  })
}
