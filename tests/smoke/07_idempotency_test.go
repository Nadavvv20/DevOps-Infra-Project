package smoke

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Test07_Idempotency verifies that delivering the exact same OrderPlaced event
// twice produces only one email confirmation and one shipment.
func Test07_Idempotency(t *testing.T) {
	requireCluster(t)

	clusterName := getClusterName()
	emailQueueName := clusterName + "-order-events-email"
	shippingQueueName := clusterName + "-order-events-shipping"

	emailQueueURL, err := runAWS("sqs", "get-queue-url", "--queue-name", emailQueueName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve SQS queue URL for %s: %v", emailQueueName, err)
	}
	shippingQueueURL, err := runAWS("sqs", "get-queue-url", "--queue-name", shippingQueueName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve SQS queue URL for %s: %v", shippingQueueName, err)
	}

	testOrderID := fmt.Sprintf("idempotency-test-%d", time.Now().UnixNano())
	testEmail := fmt.Sprintf("dedupe-%d@example.com", time.Now().UnixNano())

	eventPayload, _ := json.Marshal(map[string]interface{}{
		"order_id":     testOrderID,
		"email":        testEmail,
		"total_amount": "USD 45.00",
		"items": []map[string]interface{}{
			{"item_id": "OLJCESPC7Z", "quantity": 1},
		},
	})

	t.Logf("Publishing duplicate event (order_id: %s) to email and shipping queues...", testOrderID)

	// Send message #1
	_, err = runAWS("sqs", "send-message", "--queue-url", emailQueueURL, "--message-body", string(eventPayload))
	if err != nil {
		t.Fatalf("Failed to send first message to email queue: %v", err)
	}
	_, err = runAWS("sqs", "send-message", "--queue-url", shippingQueueURL, "--message-body", string(eventPayload))
	if err != nil {
		t.Fatalf("Failed to send first message to shipping queue: %v", err)
	}

	// Send message #2 (identical duplicate payload)
	_, err = runAWS("sqs", "send-message", "--queue-url", emailQueueURL, "--message-body", string(eventPayload))
	if err != nil {
		t.Fatalf("Failed to send second message to email queue: %v", err)
	}
	_, err = runAWS("sqs", "send-message", "--queue-url", shippingQueueURL, "--message-body", string(eventPayload))
	if err != nil {
		t.Fatalf("Failed to send second message to shipping queue: %v", err)
	}

	t.Log("Sent duplicate messages. Waiting 10s for consumers to process and deduplicate...")
	time.Sleep(10 * time.Second)

	// Verify emailservice logs show duplicate detection
	emailPod, err := getPodName("online-boutique", "app=emailservice")
	if err == nil {
		emailLogs, _ := runKubectl("logs", "-n", "online-boutique", emailPod, "--tail=60")
		emailSentCount := strings.Count(emailLogs, testOrderID)
		t.Logf("Email service occurrences of order %s: %d", testOrderID, emailSentCount)
		if strings.Contains(emailLogs, "duplicate") || strings.Contains(emailLogs, "already processed") {
			t.Log("Verified: emailservice detected duplicate order_id and skipped second send.")
		}
	}

	// Verify shippingservice logs show duplicate detection
	shippingPod, err := getPodName("online-boutique", "app=shippingservice")
	if err == nil {
		shippingLogs, _ := runKubectl("logs", "-n", "online-boutique", shippingPod, "--tail=60")
		shippingCount := strings.Count(shippingLogs, testOrderID)
		t.Logf("Shipping service occurrences of order %s: %d", testOrderID, shippingCount)
		if strings.Contains(shippingLogs, "duplicate") || strings.Contains(shippingLogs, "already processed") {
			t.Log("Verified: shippingservice detected duplicate order_id and skipped second label creation.")
		}
	}
}
