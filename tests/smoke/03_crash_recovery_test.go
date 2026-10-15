package smoke

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test03_CrashRecovery verifies that killing inventoryservice pods during hold
// expiry still results in deterministic stock restoration once restarted.
func Test03_CrashRecovery(t *testing.T) {
	requireCluster(t)
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "2ZYFJ3GM2N") // Film Camera
	t.Logf("Running Crash Recovery test for product %s", testProductID)

	// Step 1: Record initial stock
	out, err := execInPod("online-boutique", "app=inventoryservice",
		"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
	if err != nil || out == "" {
		t.Fatalf("Failed to get initial stock: %v", err)
	}
	initialStock, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	t.Logf("Initial stock: %d", initialStock)

	// Step 2: Add item to cart to create a hold
	client := createHTTPClient()
	formData := url.Values{
		"product_id": {testProductID},
		"quantity":   {"1"},
	}
	req, _ := http.NewRequest("POST", baseURL+"/cart", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to add item to cart: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Step 3: Expire the hold in Redis
	pastTimestamp := time.Now().Add(-60 * time.Second).Unix()
	script := fmt.Sprintf(`
		local members = redis.call('ZRANGE', 'holds:expiry', 0, -1)
		for _, m in ipairs(members) do
			if string.find(m, '%s') then
				redis.call('ZADD', 'holds:expiry', %d, m)
			end
		end
	`, testProductID, pastTimestamp)
	_, _ = execInPod("online-boutique", "app=inventoryservice",
		"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 EVAL \"%s\" 0", script))

	// Step 4: Kill inventoryservice pod(s) immediately while the hold is expired
	t.Log("Killing inventoryservice pod to simulate crash during expiry...")
	_, err = runKubectl("delete", "pods", "-n", "online-boutique", "-l", "app=inventoryservice", "--now")
	if err != nil {
		t.Fatalf("Failed to delete inventoryservice pods: %v", err)
	}

	// Step 5: Wait for pod to come back up healthy
	t.Log("Waiting for inventoryservice pod rollout to complete...")
	_, err = runKubectl("rollout", "status", "deployment/inventoryservice", "-n", "online-boutique", "--timeout=90s")
	if err != nil {
		t.Fatalf("inventoryservice rollout failed: %v", err)
	}

	// Step 6: Wait for reaper loop and startup reconciliation to run
	t.Log("Waiting 10s for reaper loop and reconciliation to restore stock...")
	time.Sleep(10 * time.Second)

	// Step 7: Verify stock is back to initialStock
	outAfter, _ := execInPod("online-boutique", "app=inventoryservice",
		"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
	finalStock, _ := strconv.ParseInt(strings.TrimSpace(outAfter), 10, 64)
	t.Logf("Stock after crash and recovery: %d (expected: %d)", finalStock, initialStock)
	if finalStock != initialStock {
		t.Fatalf("Crash recovery failed: expected stock %d, got %d", initialStock, finalStock)
	}
}
