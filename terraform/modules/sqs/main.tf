# ─────────────────────────────────────────────────────────────────────────────
# SQS Module — Async Event Bus for Online Boutique
#
# Creates two queues:
#  1. order-events      — primary queue; checkoutservice publishes OrderPlaced events
#  2. order-events-dlq  — dead-letter queue; messages land here after 3 failed receives
#
# Both queues use SSE-SQS encryption (no extra KMS cost for dev).
# ─────────────────────────────────────────────────────────────────────────────

# ── Dead-Letter Queue (DLQ) ───────────────────────────────────────────────────
resource "aws_sqs_queue" "order_events_dlq" {
  name                      = "${var.cluster_name}-order-events-dlq"
  message_retention_seconds = 1209600 # 14 days — maximum; gives ops time to inspect failures
  sqs_managed_sse_enabled   = true

  tags = var.tags
}

# ── Primary Order Events Queue ────────────────────────────────────────────────
resource "aws_sqs_queue" "order_events" {
  name                      = "${var.cluster_name}-order-events"
  visibility_timeout_seconds = 30      # Must be >= your consumer processing time
  message_retention_seconds = 345600   # 4 days
  sqs_managed_sse_enabled   = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.order_events_dlq.arn
    maxReceiveCount     = 3 # After 3 failed receives, message moves to DLQ
  })

  tags = var.tags
}

# ── DLQ Redrive Allow Policy ─────────────────────────────────────────────────
# Allows the primary queue to use the DLQ as its dead-letter target
resource "aws_sqs_queue_redrive_allow_policy" "order_events_dlq" {
  queue_url = aws_sqs_queue.order_events_dlq.id

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.order_events.arn]
  })
}

# ── Store Queue URL & ARN in Secrets Manager ──────────────────────────────────
# ExternalSecrets will sync these into a Kubernetes Secret so pods can read them
resource "aws_secretsmanager_secret" "sqs_config" {
  name                    = "${var.cluster_name}/sqs/order-events"
  description             = "SQS queue URL and ARN for order events"
  recovery_window_in_days = var.secret_recovery_window
  tags                    = var.tags
}

resource "aws_secretsmanager_secret_version" "sqs_config" {
  secret_id = aws_secretsmanager_secret.sqs_config.id
  secret_string = jsonencode({
    order_events_queue_url = aws_sqs_queue.order_events.url
    order_events_queue_arn = aws_sqs_queue.order_events.arn
    order_events_dlq_url   = aws_sqs_queue.order_events_dlq.url
    order_events_dlq_arn   = aws_sqs_queue.order_events_dlq.arn
  })
}
