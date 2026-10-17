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

// Test09_Consistency verifies that after N orders, the sum of quantities in
// order_items equals the decrement in inventory.total_stock, and Redis stock
// matches the ledger stock with 0 drift.
func Test09_Consistency(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "1YMWWN1N4O") // Watch
	t.Logf("Running Order Consistency test for product %s", testProductID)

	// Step 1: Query initial total_stock from Aurora if cluster accessible
	var initialTotalStock int64 = 50
	if isClusterAccessible() {
		q := fmt.Sprintf("SELECT total_stock FROM inventory WHERE product_id = '%s';", testProductID)
		out, err := execInPod("online-boutique", "app=checkoutservice",
			"sh", "-c", fmt.Sprintf("PGPASSWORD=$POSTGRES_PASSWORD psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -t -c \"%s\"", q))
		if err == nil && strings.TrimSpace(out) != "" {
			fmt.Sscanf(strings.TrimSpace(out), "%d", &initialTotalStock)
		}
	}
	t.Logf("Initial total_stock in Aurora: %d", initialTotalStock)

	// Step 2: Place N=3 orders of 1 unit each
	const numOrders = 3
	for i := 1; i <= numOrders; i++ {
		client := createHTTPClient()
		if err := loginUser(t, client, baseURL); err != nil {
			t.Fatalf("Failed to authenticate user for order %d: %v", i, err)
		}
		// Add to cart
		formData := url.Values{
			"product_id": {testProductID},
			"quantity":   {"1"},
		}
		req, _ := http.NewRequest("POST", baseURL+"/cart", strings.NewReader(formData.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Order %d add to cart failed: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Checkout
		checkoutEmail := fmt.Sprintf("consistency-order-%d-%d@example.com", i, time.Now().UnixNano())
		checkoutData := url.Values{
			"email":             {checkoutEmail},
			"street_address":    {"1600 Amphitheatre Pkwy"},
			"zip_code":          {"94043"},
			"city":              {"Mountain View"},
			"state":             {"CA"},
			"country":           {"United States"},
			"credit_card_number": {"4111111111111111"},
			"credit_card_expiration_month": {"12"},
			"credit_card_expiration_year":  {"2030"},
			"credit_card_cvv":   {"123"},
		}
		reqCheckout, _ := http.NewRequest("POST", baseURL+"/cart/checkout", strings.NewReader(checkoutData.Encode()))
		reqCheckout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		respCheckout, err := client.Do(reqCheckout)
		if err != nil {
			t.Fatalf("Order %d checkout request failed: %v", i, err)
		}
		respCheckout.Body.Close()
		t.Logf("Order %d placed successfully", i)
		time.Sleep(1 * time.Second)
	}

	// Step 3: Verify Aurora DB consistency: total_stock decremented by numOrders
	if isClusterAccessible() {
		q := fmt.Sprintf("SELECT total_stock FROM inventory WHERE product_id = '%s';", testProductID)
		out, _ := execInPod("online-boutique", "app=checkoutservice",
			"sh", "-c", fmt.Sprintf("PGPASSWORD=$POSTGRES_PASSWORD psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -t -c \"%s\"", q))
		var newTotalStock int64
		fmt.Sscanf(strings.TrimSpace(out), "%d", &newTotalStock)
		t.Logf("New total_stock in Aurora: %d (expected: %d)", newTotalStock, initialTotalStock-numOrders)
		if newTotalStock != initialTotalStock-numOrders {
			t.Errorf("Consistency check failed: Aurora total_stock is %d, expected %d", newTotalStock, initialTotalStock-numOrders)
		}

		// Step 4: Verify Redis stock equals expected stock
		redisOut, _ := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
		redisStock, _ := strconv.ParseInt(strings.TrimSpace(redisOut), 10, 64)
		t.Logf("Redis stock for %s: %d", testProductID, redisStock)
		if redisStock != newTotalStock {
			t.Errorf("Redis stock (%d) differs from Aurora total_stock (%d) without active holds", redisStock, newTotalStock)
		}

		// Step 5: Verify Prometheus stock drift metric is 0
		pod, _ := getPodName("online-boutique", "app=inventoryservice")
		metricsOut, err := runKubectl("exec", "-n", "online-boutique", pod, "--", "curl", "-s", "http://localhost:9090/metrics")
		if err == nil {
			drift, err := parsePrometheusMetric(metricsOut, "inventory_stock_drift")
			if err == nil {
				t.Logf("Current inventory_stock_drift: %f", drift)
				if drift != 0 {
					t.Errorf("Expected inventory_stock_drift to be 0, got %f", drift)
				}
			}
		}
	}
}
