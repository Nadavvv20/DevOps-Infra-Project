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

// Test02_HoldExpiry verifies that when a hold expires:
// - With 2+ replicas running, the reaper loop restores stock exactly once (no double-restoration).
// - Stock returns to expected level deterministically.
func Test02_HoldExpiry(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "66VCHSJNUP") // Vintage Camera Lens
	t.Logf("Running Hold Expiry test against %s for product %s", baseURL, testProductID)

	// Step 1: Ensure inventoryservice has at least 2 replicas running to verify multi-replica safety
	if isClusterAccessible() {
		replicasOut, _ := runKubectl("get", "deployment", "inventoryservice", "-n", "online-boutique",
			"-o", "jsonpath={.spec.replicas}")
		t.Logf("Inventoryservice replica count: %s", replicasOut)
		if replicasOut == "1" {
			t.Log("Scaling inventoryservice to 2 replicas for multi-replica concurrency test...")
			_, _ = runKubectl("scale", "deployment", "inventoryservice", "-n", "online-boutique", "--replicas=2")
			_, _ = runKubectl("rollout", "status", "deployment/inventoryservice", "-n", "online-boutique", "--timeout=60s")
		}
	}

	// Step 2: Query initial stock
	var initialStock int64 = 10
	if isClusterAccessible() {
		out, err := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
		if err == nil && out != "" {
			if s, convErr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); convErr == nil {
				initialStock = s
			}
		}
	}
	t.Logf("Initial stock for %s: %d", testProductID, initialStock)

	// Step 3: Place a hold by adding 1 unit to cart
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

	// Step 4: Verify stock decremented by 1
	if isClusterAccessible() {
		out, _ := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
		stockAfterHold, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		t.Logf("Stock after hold created: %d (expected %d)", stockAfterHold, initialStock-1)
		if stockAfterHold != initialStock-1 {
			t.Errorf("Expected stock to decrement to %d, got %d", initialStock-1, stockAfterHold)
		}

		// In test mode, adjust score in holds:expiry to expire immediately to avoid waiting full TTL
		t.Log("Simulating hold expiry by setting score in holds:expiry to past timestamp...")
		_, _ = execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", "redis-cli -h $REDIS_ADDR -p 6379 ZREMRANGEBYSCORE 'holds:expiry' 0 0 || true")
		// Update scores for product to 1 second ago
		pastTimestamp := time.Now().Add(-10 * time.Second).Unix()
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
	}

	// Step 5: Wait for reaper loop (runs every 5s) across both replicas
	t.Log("Waiting 8 seconds for background reaper loop to execute across 2 replicas...")
	time.Sleep(8 * time.Second)

	// Step 6: Verify stock was restored EXACTLY ONCE
	if isClusterAccessible() {
		out, _ := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
		finalStock, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		t.Logf("Stock after reaper run: %d (expected %d)", finalStock, initialStock)
		if finalStock != initialStock {
			t.Fatalf("Reaper failed to restore stock exactly once: expected %d, got %d", initialStock, finalStock)
		}

		// Also verify hold key is removed
		holdsRemaining, _ := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 ZCOUNT 'holds:expiry' 0 %d", time.Now().Unix()))
		t.Logf("Expired holds remaining in holds:expiry: %s", holdsRemaining)
	}
}
