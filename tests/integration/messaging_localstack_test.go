package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/redis/go-redis/v9"
	"github.com/GoogleCloudPlatform/microservices-demo/src/shippingservice/pkg/consumer"
)

func getLocalStackConfig(ctx context.Context) (aws.Config, error) {
	endpoint := os.Getenv("LOCALSTACK_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4566"
	}
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:       "aws",
			URL:               endpoint,
			SigningRegion:     "il-central-1",
			HostnameImmutable: true,
		}, nil
	})

	return awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("il-central-1"),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
		awsconfig.WithCredentialsProvider(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     "test",
				SecretAccessKey: "test",
			}, nil
		})),
	)
}

func getRealRedisClient() *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	return redis.NewClient(&redis.Options{Addr: addr})
}

// TestFanoutAndDLQLogic exercises real LocalStack SNS fanout and SQS RedrivePolicy (maxReceiveCount=3)
func TestFanoutAndDLQLogic(t *testing.T) {
	ctx := context.Background()
	cfg, err := getLocalStackConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to load AWS config for LocalStack: %v", err)
	}

	snsClient := sns.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	// 1. Create SNS Topic
	topicName := fmt.Sprintf("order-events-test-%d", time.Now().UnixNano())
	topicRes, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: aws.String(topicName),
	})
	if err != nil {
		t.Fatalf("Failed to create SNS topic: %v", err)
	}
	topicArn := *topicRes.TopicArn
	t.Logf("Created SNS Topic: %s", topicArn)

	// 2. Create SQS Dead Letter Queue (DLQ)
	dlqName := fmt.Sprintf("email-dlq-test-%d", time.Now().UnixNano())
	dlqRes, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(dlqName),
	})
	if err != nil {
		t.Fatalf("Failed to create DLQ: %v", err)
	}
	dlqUrl := *dlqRes.QueueUrl

	dlqAttrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(dlqUrl),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("Failed to get DLQ ARN: %v", err)
	}
	dlqArn := dlqAttrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	t.Logf("Created SQS DLQ: %s (ARN: %s)", dlqUrl, dlqArn)

	// 3. Create Main Queue with RedrivePolicy (maxReceiveCount = 3)
	mainQueueName := fmt.Sprintf("email-queue-test-%d", time.Now().UnixNano())
	redrivePolicy := fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":"3"}`, dlqArn)
	mainQueueRes, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(mainQueueName),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameRedrivePolicy): redrivePolicy,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create main queue with RedrivePolicy: %v", err)
	}
	mainQueueUrl := *mainQueueRes.QueueUrl

	mainAttrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(mainQueueUrl),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("Failed to get main queue ARN: %v", err)
	}
	mainQueueArn := mainAttrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	t.Logf("Created Main SQS Queue: %s with maxReceiveCount=3", mainQueueUrl)

	// 4. Subscribe Main Queue to SNS Topic
	_, err = snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicArn),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(mainQueueArn),
		Attributes: map[string]string{
			"RawMessageDelivery": "true",
		},
	})
	if err != nil {
		t.Fatalf("Failed to subscribe main queue to SNS topic: %v", err)
	}

	// 5. Publish Poison Message to SNS
	poisonPayload := `{"order_id":"poison-999","corrupted":true}`
	pubRes, err := snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(topicArn),
		Message:  aws.String(poisonPayload),
	})
	if err != nil {
		t.Fatalf("Failed to publish poison message: %v", err)
	}
	t.Logf("Published poison message to SNS: %s", *pubRes.MessageId)

	// 6. Simulate 3 failed receives without deleting message (resetting visibility to 0 to retry immediately)
	for attempt := 1; attempt <= 3; attempt++ {
		t.Logf("Attempt %d: Polling main queue for poison message...", attempt)
		var msg *sqstypes.Message
		for retries := 0; retries < 10; retries++ {
			recvRes, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:                    aws.String(mainQueueUrl),
				MaxNumberOfMessages:         1,
				WaitTimeSeconds:             2,
				VisibilityTimeout:           10,
				MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameApproximateReceiveCount},
			})
			if err == nil && len(recvRes.Messages) > 0 {
				msg = &recvRes.Messages[0]
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if msg == nil {
			t.Fatalf("Expected message on receive attempt %d, but queue was empty", attempt)
		}

		rc := msg.Attributes[string(sqstypes.MessageSystemAttributeNameApproximateReceiveCount)]
		t.Logf("Received message on attempt %d: ID=%s, ApproximateReceiveCount=%s", attempt, *msg.MessageId, rc)

		// Reset visibility to 0 so SQS delivers it again immediately without deleting
		_, err = sqsClient.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
			QueueUrl:          aws.String(mainQueueUrl),
			ReceiptHandle:     msg.ReceiptHandle,
			VisibilityTimeout: 0,
		})
		if err != nil {
			t.Logf("ChangeMessageVisibility warning: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 7. On 4th receive, main queue should be empty (message moved to DLQ by LocalStack!)
	t.Log("Attempt 4: Checking that message has been removed from main queue...")
	recvMain, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(mainQueueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     2,
	})
	if err == nil && len(recvMain.Messages) > 0 {
		t.Errorf("Expected main queue to be empty after exceeding maxReceiveCount=3, but got message: %s", *recvMain.Messages[0].MessageId)
	} else {
		t.Log("Verified: Main queue is empty after maxReceiveCount=3 failures.")
	}

	// 8. Verify message arrived in DLQ
	t.Log("Checking DLQ for routed poison message...")
	var dlqMsg *sqstypes.Message
	for retries := 0; retries < 10; retries++ {
		recvDLQ, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(dlqUrl),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     2,
		})
		if err == nil && len(recvDLQ.Messages) > 0 {
			dlqMsg = &recvDLQ.Messages[0]
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if dlqMsg == nil {
		t.Fatalf("Poison message did not arrive in DLQ!")
	}
	t.Logf("SUCCESS: Poison message landed in DLQ! MessageId=%s, Body=%s", *dlqMsg.MessageId, *dlqMsg.Body)

	// Clean up DLQ message
	_, _ = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(dlqUrl),
		ReceiptHandle: dlqMsg.ReceiptHandle,
	})
}

// TestConsumerSideEffectFailureAndRetry verifies with production consumer package that:
// 1. If side effect fails, short-TTL :processing lock is released and message is NOT marked :done.
// 2. Message is NOT deleted from SQS, allowing SQS retry.
// 3. Subsequent worker attempt succeeds, executes side effect, sets :done, and deletes message.
func TestConsumerSideEffectFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	cfg, err := getLocalStackConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to load AWS config for LocalStack: %v", err)
	}

	sqsClient := sqs.NewFromConfig(cfg)
	rdb := getRealRedisClient()
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("Real Redis is not reachable at localhost:6379: %v", err)
	}

	queueName := fmt.Sprintf("test-shipping-retry-%d", time.Now().UnixNano())
	qRes, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		t.Fatalf("Failed to create test SQS queue: %v", err)
	}
	queueUrl := *qRes.QueueUrl

	testOrderID := fmt.Sprintf("order-retry-%d", time.Now().UnixNano())
	doneKey := fmt.Sprintf("processed_order:shipping:%s:done", testOrderID)
	procKey := fmt.Sprintf("processed_order:shipping:%s:processing", testOrderID)

	// Clean any previous test keys
	rdb.Del(ctx, doneKey, procKey)

	// Send OrderPlaced message to SQS
	payloadBytes, _ := json.Marshal(map[string]interface{}{
		"order_id":             testOrderID,
		"shipping_tracking_id": "TRK-REAL-9999",
	})
	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueUrl),
		MessageBody: aws.String(string(payloadBytes)),
	})
	if err != nil {
		t.Fatalf("Failed to send message to SQS: %v", err)
	}

	// ── WORKER ATTEMPT 1: Side Effect FAILS ──────────────────────────────────────
	recv1, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil || len(recv1.Messages) == 0 {
		t.Fatalf("Worker 1 failed to receive message from SQS: %v", err)
	}
	msg1 := recv1.Messages[0]

	// Call production consumer.ProcessShippingOrderEvent with a failing side effect
	status1, _, err1 := consumer.ProcessShippingOrderEvent(
		ctx,
		rdb,
		[]byte(*msg1.Body),
		func(orderID, trackingID string) error {
			return fmt.Errorf("simulated warehouse API 503 error")
		},
	)
	if status1 != consumer.StatusSideEffectFailed {
		t.Fatalf("Expected StatusSideEffectFailed, got %v (err: %v)", status1, err1)
	}

	// Verify :processing lock is cleaned up and :done is NOT set
	procExists, _ := rdb.Exists(ctx, procKey).Result()
	if procExists > 0 {
		t.Errorf("Processing key should be deleted when side effect fails")
	}
	doneExists, _ := rdb.Exists(ctx, doneKey).Result()
	if doneExists > 0 {
		t.Fatalf("Done key should NOT be set when side effect fails")
	}

	// DO NOT delete message from SQS (fail-closed); reset visibility to 0 for immediate retry
	_, _ = sqsClient.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(queueUrl),
		ReceiptHandle:     msg1.ReceiptHandle,
		VisibilityTimeout: 0,
	})

	// ── WORKER ATTEMPT 2: Redrive & Side Effect SUCCEEDS ─────────────────────────
	recv2, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil || len(recv2.Messages) == 0 {
		t.Fatalf("Worker 2 failed to receive redriven message from SQS: %v", err)
	}
	msg2 := recv2.Messages[0]

	var sideEffectRan bool
	status2, _, err2 := consumer.ProcessShippingOrderEvent(
		ctx,
		rdb,
		[]byte(*msg2.Body),
		func(orderID, trackingID string) error {
			sideEffectRan = true
			return nil
		},
	)
	if status2 != consumer.StatusSuccess {
		t.Fatalf("Expected StatusSuccess on retry, got %v (err: %v)", status2, err2)
	}
	if !sideEffectRan {
		t.Fatalf("Expected side effect to run on worker 2")
	}

	// Verify long-TTL done key is set in Redis
	doneFinal, _ := rdb.Exists(ctx, doneKey).Result()
	if doneFinal == 0 {
		t.Errorf("Expected done marker to exist in Redis after successful processing")
	}

	// Delete message after success
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueUrl),
		ReceiptHandle: msg2.ReceiptHandle,
	})
	if err != nil {
		t.Fatalf("Failed to delete SQS message: %v", err)
	}

	t.Log("SUCCESS: Verified production consumer package two-phase dedup and safe retry against real LocalStack & Redis.")
}

// TestConsumerProcessingMarkerLockedByAnotherWorker verifies Requirement 6:
// When a message is received and the :processing marker is held by another worker,
// the consumer must NOT delete the message and must NOT process it.
func TestConsumerProcessingMarkerLockedByAnotherWorker(t *testing.T) {
	ctx := context.Background()
	cfg, err := getLocalStackConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to load AWS config for LocalStack: %v", err)
	}

	sqsClient := sqs.NewFromConfig(cfg)
	rdb := getRealRedisClient()
	defer rdb.Close()

	queueName := fmt.Sprintf("test-shipping-locked-%d", time.Now().UnixNano())
	qRes, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		t.Fatalf("Failed to create test SQS queue: %v", err)
	}
	queueUrl := *qRes.QueueUrl

	testOrderID := fmt.Sprintf("order-locked-%d", time.Now().UnixNano())
	procKey := fmt.Sprintf("processed_order:shipping:%s:processing", testOrderID)
	doneKey := fmt.Sprintf("processed_order:shipping:%s:done", testOrderID)

	// Another worker holds the :processing lock in Redis
	err = rdb.Set(ctx, procKey, "worker-alpha", 60*time.Second).Err()
	if err != nil {
		t.Fatalf("Failed to set processing lock: %v", err)
	}
	defer rdb.Del(ctx, procKey, doneKey)

	// Send OrderPlaced message to SQS
	payloadBytes, _ := json.Marshal(map[string]interface{}{
		"order_id":             testOrderID,
		"shipping_tracking_id": "TRK-LOCKED-1111",
	})
	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueUrl),
		MessageBody: aws.String(string(payloadBytes)),
	})
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Worker receives message from SQS
	recv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil || len(recv.Messages) == 0 {
		t.Fatalf("Failed to receive message from SQS: %v", err)
	}
	msg := recv.Messages[0]

	// Worker calls production consumer.ProcessShippingOrderEvent
	sideEffectExecuted := false
	status, _, err := consumer.ProcessShippingOrderEvent(
		ctx,
		rdb,
		[]byte(*msg.Body),
		func(orderID, trackingID string) error {
			sideEffectExecuted = true
			return nil
		},
	)

	// 1. Must return StatusProcessingLocked
	if status != consumer.StatusProcessingLocked {
		t.Errorf("Expected StatusProcessingLocked, got %v (err: %v)", status, err)
	}

	// 2. Side effect must NOT have executed
	if sideEffectExecuted {
		t.Errorf("Side effect must NOT execute when :processing is held by another worker")
	}

	// 3. Worker must NOT delete the message from SQS
	// Reset visibility so we can verify message is still in queue
	_, _ = sqsClient.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(queueUrl),
		ReceiptHandle:     msg.ReceiptHandle,
		VisibilityTimeout: 0,
	})

	verifyRecv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     3,
	})
	if err != nil || len(verifyRecv.Messages) == 0 {
		t.Fatalf("Message was deleted or missing from SQS! It must be retained in queue.")
	}

	t.Log("SUCCESS: Verified that message with locked :processing marker was NOT processed and NOT deleted from SQS.")
}
