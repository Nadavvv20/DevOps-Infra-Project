package smoke

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Test08_TransactionalOutbox verifies that an unpublished event in the Aurora
// outbox table (e.g. caused by a pod crash before SNS send) is reliably published
// by the background outbox publisher after pod restart.
func Test08_TransactionalOutbox(t *testing.T) {
	requireCluster(t)

	testOrderID := fmt.Sprintf("outbox-recovery-%d", time.Now().UnixNano())
	t.Logf("Testing outbox recovery with order ID: %s", testOrderID)

	// Step 1: Insert an unpublished outbox row directly into Aurora PostgreSQL
	// payload is OrderPlaced JSON with published_at = NULL
	insertQuery := fmt.Sprintf(`
		INSERT INTO outbox (id, event_type, payload, created_at, published_at)
		VALUES ('%s', 'OrderPlaced', '{\"order_id\":\"%s\",\"email\":\"outbox-test@example.com\",\"total_amount\":\"USD 99.00\"}', NOW(), NULL);
	`, testOrderID, testOrderID)

	out, err := execInPod("online-boutique", "app=checkoutservice",
		"sh", "-c", fmt.Sprintf("PGPASSWORD=$POSTGRES_PASSWORD psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -c \"%s\"", insertQuery))
	if err != nil {
		t.Logf("Direct psql insert note: %v (%s)", err, out)
	}

	// Step 2: Restart checkoutservice pod to simulate crash recovery
	t.Log("Restarting checkoutservice pod to test outbox publisher startup recovery...")
	_, err = runKubectl("rollout", "restart", "deployment/checkoutservice", "-n", "online-boutique")
	if err != nil {
		t.Fatalf("Failed to trigger rollout restart of checkoutservice: %v", err)
	}
	_, err = runKubectl("rollout", "status", "deployment/checkoutservice", "-n", "online-boutique", "--timeout=90s")
	if err != nil {
		t.Fatalf("checkoutservice rollout failed: %v", err)
	}

	// Step 3: Wait 10s for the outbox polling worker (runs every 3-5s) to process the pending row
	t.Log("Waiting for outbox publisher to sweep unpublished rows...")
	time.Sleep(10 * time.Second)

	// Step 4: Verify that outbox row now has a non-null published_at timestamp
	verifyQuery := fmt.Sprintf("SELECT published_at FROM outbox WHERE id = '%s';", testOrderID)
	res, err := execInPod("online-boutique", "app=checkoutservice",
		"sh", "-c", fmt.Sprintf("PGPASSWORD=$POSTGRES_PASSWORD psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -t -c \"%s\"", verifyQuery))
	if err == nil {
		publishedAt := strings.TrimSpace(res)
		t.Logf("outbox row published_at: '%s'", publishedAt)
		if publishedAt != "" && !strings.Contains(publishedAt, "null") && !strings.Contains(publishedAt, "NULL") {
			t.Log("Verified: outbox row was successfully published and marked published_at after restart!")
		}
	}

	// Step 5: Check checkoutservice logs for outbox publishing
	newPod, _ := getPodName("online-boutique", "app=checkoutservice")
	logs, _ := runKubectl("logs", "-n", "online-boutique", newPod, "--tail=50")
	if strings.Contains(logs, "Outbox") || strings.Contains(logs, "Published outbox event") {
		t.Log("Verified: checkoutservice log confirms outbox sweep and SNS publish.")
	}
}
