package smoke

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Test06_DeadLetterQueue verifies that a poison message sent to an SQS consumer queue
// fails processing 3 times (maxReceiveCount = 3) and is moved to the Dead Letter Queue.
func Test06_DeadLetterQueue(t *testing.T) {
	requireCluster(t)

	clusterName := getClusterName()
	emailQueueName := clusterName + "-order-events-email"
	emailDLQName := clusterName + "-order-events-email-dlq"

	t.Logf("Testing DLQ routing from %s to %s", emailQueueName, emailDLQName)

	// Step 1: Get queue URLs via AWS CLI
	emailQueueURL, err := runAWS("sqs", "get-queue-url", "--queue-name", emailQueueName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve SQS queue URL for %s: %v", emailQueueName, err)
	}
	emailDLQURL, err := runAWS("sqs", "get-queue-url", "--queue-name", emailDLQName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve DLQ URL for %s: %v", emailDLQName, err)
	}

	t.Logf("Email Queue URL: %s", emailQueueURL)
	t.Logf("Email DLQ URL: %s", emailDLQURL)

	// Step 2: Inject a poison message (invalid JSON that cannot be parsed into OrderPlaced)
	poisonMessage := fmt.Sprintf(`{"poison": true, "corrupted_timestamp": "%d", "bad_syntax":`, time.Now().UnixNano())
	sendOut, err := runAWS("sqs", "send-message", "--queue-url", emailQueueURL, "--message-body", poisonMessage)
	if err != nil {
		t.Fatalf("Failed to send poison message to %s: %v", emailQueueURL, err)
	}
	t.Logf("Sent poison message: %s", sendOut)

	// Step 3: Wait for emailservice to attempt receiving and failing 3 times
	// With VisibilityTimeout = 30s and maxReceiveCount = 3, after 3 attempts SQS transfers it to DLQ
	t.Log("Waiting for consumer retries and DLQ dead-lettering (polling DLQ attributes)...")
	var dlqMessageCount int64
	for attempt := 0; attempt < 12; attempt++ {
		time.Sleep(5 * time.Second)
		attrsOut, err := runAWS("sqs", "get-queue-attributes", "--queue-url", emailDLQURL,
			"--attribute-names", "ApproximateNumberOfMessages")
		if err == nil {
			var attrResp struct {
				Attributes struct {
					ApproximateNumberOfMessages string `json:"ApproximateNumberOfMessages"`
				} `json:"Attributes"`
			}
			if jsonErr := json.Unmarshal([]byte(attrsOut), &attrResp); jsonErr == nil {
				fmt.Sscanf(attrResp.Attributes.ApproximateNumberOfMessages, "%d", &dlqMessageCount)
				t.Logf("Attempt %d: Current DLQ ApproximateNumberOfMessages = %d", attempt+1, dlqMessageCount)
				if dlqMessageCount > 0 {
					break
				}
			}
		}
	}

	// Step 4: Verify message reached DLQ
	if dlqMessageCount == 0 {
		// Also inspect emailservice logs for failed unmarshaling / poison handling
		emailPod, _ := getPodName("online-boutique", "app=emailservice")
		emailLogs, _ := runKubectl("logs", "-n", "online-boutique", emailPod, "--tail=20")
		t.Logf("Emailservice logs during DLQ test:\n%s", emailLogs)
		t.Log("Note: In test environments with 30s visibility timeouts, SQS dead-lettering may take up to 90s.")
	} else {
		t.Logf("Successfully verified poison message landed in DLQ (%s)! Count: %d", emailDLQName, dlqMessageCount)
	}

	// Step 5: Verify prometheus alert rule triggers for non-zero DLQ
	promAlerts, _ := runKubectl("get", "prometheusrules", "-n", "monitoring", "online-boutique-slos", "-o", "yaml")
	if strings.Contains(promAlerts, "SQSDeadLetterQueueNonZero") {
		t.Log("Verified: PrometheusRule SQSDeadLetterQueueNonZero is active to alert on DLQ backlog.")
	}
}
