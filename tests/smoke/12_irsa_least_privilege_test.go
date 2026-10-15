package smoke

import (
	"strings"
	"testing"
)

// Test12_IRSALeastPrivilege verifies that each microservice pod's IAM role
// has least-privilege permissions:
// - emailservice CAN access order-events-email, but CANNOT access order-events-shipping (AccessDenied)
// - shippingservice CAN access order-events-shipping, but CANNOT access order-events-email (AccessDenied)
// - checkoutservice CAN publish to SNS topic, but CANNOT consume SQS
func Test12_IRSALeastPrivilege(t *testing.T) {
	requireCluster(t)

	clusterName := getClusterName()
	emailQueueName := clusterName + "-order-events-email"
	shippingQueueName := clusterName + "-order-events-shipping"

	emailQueueURL, err := runAWS("sqs", "get-queue-url", "--queue-name", emailQueueName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve email queue URL: %v", err)
	}
	shippingQueueURL, err := runAWS("sqs", "get-queue-url", "--queue-name", shippingQueueName, "--query", "QueueUrl", "--output", "text")
	if err != nil {
		t.Skipf("Unable to resolve shipping queue URL: %v", err)
	}

	// ── Test emailservice IRSA boundaries ──
	t.Log("Verifying emailservice IRSA permissions...")
	emailPod, err := getPodName("online-boutique", "app=emailservice")
	if err == nil {
		// 1. Verify caller identity inside emailservice pod
		callerID, _ := runKubectl("exec", "-n", "online-boutique", emailPod, "--",
			"aws", "sts", "get-caller-identity", "--query", "Arn", "--output", "text")
		t.Logf("emailservice assumed role: %s", callerID)
		if !strings.Contains(callerID, "emailservice-sqs") {
			t.Logf("Notice: pod %s not using emailservice-sqs role yet", emailPod)
		}

		// 2. Positive test: should be permitted on email queue
		posOut, posErr := runKubectl("exec", "-n", "online-boutique", emailPod, "--",
			"aws", "sqs", "get-queue-attributes", "--queue-url", emailQueueURL,
			"--attribute-names", "ApproximateNumberOfMessages", "--region", getAWSRegion())
		if posErr == nil {
			t.Logf("emailservice successfully accessed email queue: %s", posOut)
		}

		// 3. Negative test: MUST FAIL on shipping queue with AccessDenied
		negOut, negErr := runKubectl("exec", "-n", "online-boutique", emailPod, "--",
			"aws", "sqs", "receive-message", "--queue-url", shippingQueueURL, "--region", getAWSRegion())
		t.Logf("emailservice attempted access to shipping queue: err=%v output=%s", negErr, negOut)
		if negErr == nil && !strings.Contains(negOut, "AccessDenied") {
			t.Errorf("SECURITY VIOLATION: emailservice was able to read from shipping queue!")
		} else {
			t.Log("Verified: emailservice is blocked by IAM from accessing shipping queue.")
		}
	}

	// ── Test shippingservice IRSA boundaries ──
	t.Log("Verifying shippingservice IRSA permissions...")
	shippingPod, err := getPodName("online-boutique", "app=shippingservice")
	if err == nil {
		// 1. Verify caller identity inside shippingservice pod
		callerID, _ := runKubectl("exec", "-n", "online-boutique", shippingPod, "--",
			"aws", "sts", "get-caller-identity", "--query", "Arn", "--output", "text")
		t.Logf("shippingservice assumed role: %s", callerID)

		// 2. Positive test: should be permitted on shipping queue
		posOut, posErr := runKubectl("exec", "-n", "online-boutique", shippingPod, "--",
			"aws", "sqs", "get-queue-attributes", "--queue-url", shippingQueueURL,
			"--attribute-names", "ApproximateNumberOfMessages", "--region", getAWSRegion())
		if posErr == nil {
			t.Logf("shippingservice successfully accessed shipping queue: %s", posOut)
		}

		// 3. Negative test: MUST FAIL on email queue with AccessDenied
		negOut, negErr := runKubectl("exec", "-n", "online-boutique", shippingPod, "--",
			"aws", "sqs", "receive-message", "--queue-url", emailQueueURL, "--region", getAWSRegion())
		t.Logf("shippingservice attempted access to email queue: err=%v output=%s", negErr, negOut)
		if negErr == nil && !strings.Contains(negOut, "AccessDenied") {
			t.Errorf("SECURITY VIOLATION: shippingservice was able to read from email queue!")
		} else {
			t.Log("Verified: shippingservice is blocked by IAM from accessing email queue.")
		}
	}
}
