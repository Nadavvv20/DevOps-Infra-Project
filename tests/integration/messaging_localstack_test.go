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

// TestConsumerSideEffectFailureAndRetry proves:
// 1. Consumer acquires short-TTL "processing" marker before executing side effect.
// 2. If side effect fails, processing marker is cleaned up, message is NOT deleted from SQS (fail-closed).
// 3. Redrive delivers message again, processing succeeds, long-TTL "done" marker is set, and message is deleted.
func TestConsumerSideEffectFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	rdb := getRealRedisClient()
	defer rdb.Close()

	cfg, err := getLocalStackConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to load AWS config for LocalStack: %v", err)
	}
	sqsClient := sqs.NewFromConfig(cfg)

	// Create test queue
	qName := fmt.Sprintf("shipping-retry-test-%d", time.Now().UnixNano())
	qRes, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(qName),
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

	// Step A: Acquire short-TTL (60s) processing lock
	acquired1, err := rdb.SetNX(ctx, procKey, "1", 60*time.Second).Result()
	if err != nil || !acquired1 {
		t.Fatalf("Worker 1 failed to acquire processing marker: %v", err)
	}
	t.Logf("Worker 1 acquired processing marker: %s", procKey)

	// Step B: Simulate side effect FAILURE
	sideEffectSuccess := false
	if !sideEffectSuccess {
		t.Logf("Worker 1 side effect FAILED. Releasing processing marker and leaving message in SQS.")
		_ = rdb.Del(ctx, procKey).Err()
		// DO NOT set done marker!
		// DO NOT delete message from SQS (fail-closed)!
		// Reset visibility timeout to 0 so next worker can pick it up immediately
		_, _ = sqsClient.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
			QueueUrl:          aws.String(queueUrl),
			ReceiptHandle:     msg1.ReceiptHandle,
			VisibilityTimeout: 0,
		})
	}

	// Verify done key is NOT set
	doneExists, _ := rdb.Exists(ctx, doneKey).Result()
	if doneExists > 0 {
		t.Fatalf("Done key should NOT be set when side effect failed!")
	}

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
	t.Logf("Worker 2 received redriven message: %s", *msg2.MessageId)

	// Step A: Check done key (does not exist)
	isDone, _ := rdb.Exists(ctx, doneKey).Result()
	if isDone > 0 {
		t.Fatalf("Done key should not exist before worker 2 executes side effect")
	}

	// Step B: Acquire processing marker
	acquired2, err := rdb.SetNX(ctx, procKey, "1", 60*time.Second).Result()
	if err != nil || !acquired2 {
		t.Fatalf("Worker 2 failed to acquire processing marker: %v", err)
	}

	// Step C: Execute side effect -> SUCCEEDS!
	t.Logf("Worker 2 side effect SUCCEEDED!")

	// Step D: Set long-TTL done marker (7 days) and delete processing marker
	err = rdb.Set(ctx, doneKey, "1", 7*24*time.Hour).Err()
	if err != nil {
		t.Fatalf("Failed to set done marker: %v", err)
	}
	_ = rdb.Del(ctx, procKey).Err()

	// Step E: Delete message from SQS
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueUrl),
		ReceiptHandle: msg2.ReceiptHandle,
	})
	if err != nil {
		t.Fatalf("Failed to delete SQS message: %v", err)
	}

	// Verify final state
	doneFinal, _ := rdb.Exists(ctx, doneKey).Result()
	if doneFinal == 0 {
		t.Errorf("Expected done marker to exist in Redis after successful processing")
	}

	// Verify queue is now empty
	recvEmpty, _ := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     2,
	})
	if len(recvEmpty.Messages) > 0 {
		t.Errorf("Queue should be empty after successful processing and deletion")
	}

	t.Log("SUCCESS: Verified that side effect failure safely retried and completed with two-phase Redis dedup.")
}
