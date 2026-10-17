#!/bin/bash
set -eo pipefail

echo "=========================================================="
echo "Initializing LocalStack SNS and SQS for Online Boutique..."
echo "=========================================================="
export AWS_DEFAULT_REGION="il-central-1"
export AWS_REGION="il-central-1"

# 1. Create Dead-Letter Queues (DLQs) matching Terraform names
echo "Creating email DLQ (online-boutique-dev-order-events-email-dlq)..."
awslocal sqs create-queue --queue-name online-boutique-dev-order-events-email-dlq

EMAIL_DLQ_URL=$(awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-email-dlq --query "QueueUrl" --output text)
EMAIL_DLQ_ARN=$(awslocal sqs get-queue-attributes --queue-url "$EMAIL_DLQ_URL" --attribute-names QueueArn --query "Attributes.QueueArn" --output text)

echo "Creating shipping DLQ (online-boutique-dev-order-events-shipping-dlq)..."
awslocal sqs create-queue --queue-name online-boutique-dev-order-events-shipping-dlq

SHIPPING_DLQ_URL=$(awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-shipping-dlq --query "QueueUrl" --output text)
SHIPPING_DLQ_ARN=$(awslocal sqs get-queue-attributes --queue-url "$SHIPPING_DLQ_URL" --attribute-names QueueArn --query "Attributes.QueueArn" --output text)

# 2. Create Main Queues with Redrive Policy (maxReceiveCount = 3)
echo "Creating email queue (online-boutique-dev-order-events-email)..."
awslocal sqs create-queue \
  --queue-name online-boutique-dev-order-events-email \
  --attributes "{\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${EMAIL_DLQ_ARN}\\\",\\\"maxReceiveCount\\\":\\\"3\\\"}\",\"VisibilityTimeout\":\"30\"}"

EMAIL_QUEUE_URL=$(awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-email --query "QueueUrl" --output text)
EMAIL_QUEUE_ARN=$(awslocal sqs get-queue-attributes --queue-url "$EMAIL_QUEUE_URL" --attribute-names QueueArn --query "Attributes.QueueArn" --output text)

echo "Creating shipping queue (online-boutique-dev-order-events-shipping)..."
awslocal sqs create-queue \
  --queue-name online-boutique-dev-order-events-shipping \
  --attributes "{\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${SHIPPING_DLQ_ARN}\\\",\\\"maxReceiveCount\\\":\\\"3\\\"}\",\"VisibilityTimeout\":\"30\"}"

SHIPPING_QUEUE_URL=$(awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-shipping --query "QueueUrl" --output text)
SHIPPING_QUEUE_ARN=$(awslocal sqs get-queue-attributes --queue-url "$SHIPPING_QUEUE_URL" --attribute-names QueueArn --query "Attributes.QueueArn" --output text)

# 3. Create SNS Topic matching Terraform name
echo "Creating SNS topic (online-boutique-dev-order-events)..."
TOPIC_ARN=$(awslocal sns create-topic --name online-boutique-dev-order-events --query "TopicArn" --output text)

# 4. Subscribe Queues to SNS Topic with RawMessageDelivery
echo "Subscribing email queue to topic..."
awslocal sns subscribe \
  --topic-arn "$TOPIC_ARN" \
  --protocol sqs \
  --notification-endpoint "$EMAIL_QUEUE_ARN" \
  --attributes RawMessageDelivery=true

echo "Subscribing shipping queue to topic..."
awslocal sns subscribe \
  --topic-arn "$TOPIC_ARN" \
  --protocol sqs \
  --notification-endpoint "$SHIPPING_QUEUE_ARN" \
  --attributes RawMessageDelivery=true

echo "=========================================================="
echo "LocalStack SNS/SQS initialization verified:"
echo "SNS Topic: ${TOPIC_ARN}"
echo "Email Queue: ${EMAIL_QUEUE_URL} (${EMAIL_QUEUE_ARN})"
echo "Shipping Queue: ${SHIPPING_QUEUE_URL} (${SHIPPING_QUEUE_ARN})"
echo "Email DLQ: ${EMAIL_DLQ_URL} (${EMAIL_DLQ_ARN})"
echo "Shipping DLQ: ${SHIPPING_DLQ_URL} (${SHIPPING_DLQ_ARN})"
echo "=========================================================="
