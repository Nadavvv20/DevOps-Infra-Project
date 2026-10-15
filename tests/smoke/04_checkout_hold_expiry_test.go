package smoke

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Test04_CheckoutAfterHoldExpiry verifies that checkout is rejected
// BEFORE any payment call is attempted if cart item holds have expired.
func Test04_CheckoutAfterHoldExpiry(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "L9ECAV7KIM") // Terrarium
	t.Logf("Running Checkout After Hold Expiry test against %s", baseURL)

	client := createHTTPClient()

	// Step 1: Add item to cart
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

	// Step 2: Manually remove or expire the hold from Redis to simulate hold expiration
	if isClusterAccessible() {
		t.Log("Simulating hold expiration by removing active hold from Redis...")
		_, _ = execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 EVAL \""+
				"local keys = redis.call('KEYS', 'hold:{%s}:*') "+
				"for _, k in ipairs(keys) do redis.call('DEL', k) end "+
				"local exp = redis.call('ZRANGE', 'holds:expiry', 0, -1) "+
				"for _, e in ipairs(exp) do if string.find(e, '%s') then redis.call('ZREM', 'holds:expiry', e) end end"+
				"\" 0", testProductID, testProductID))
	}

	// Step 3: Attempt to complete checkout
	checkoutData := url.Values{
		"email":             {"test-expired@example.com"},
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
		t.Fatalf("Checkout request failed: %v", err)
	}
	defer respCheckout.Body.Close()
	body, _ := io.ReadAll(respCheckout.Body)
	bodyStr := string(body)

	// Step 4: Verify order was rejected
	t.Logf("Checkout response status: %d", respCheckout.StatusCode)
	if !strings.Contains(bodyStr, "expired") && !strings.Contains(bodyStr, "reservation") &&
		!strings.Contains(bodyStr, "FailedPrecondition") && !strings.Contains(bodyStr, "could not be placed") &&
		respCheckout.StatusCode != http.StatusBadRequest && respCheckout.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected checkout to be rejected due to expired hold, but got status %d", respCheckout.StatusCode)
	}

	// Step 5: Verify payment was NOT charged (inspect paymentservice logs if cluster accessible)
	if isClusterAccessible() {
		logs, _ := execInPod("online-boutique", "app=paymentservice",
			"sh", "-c", "grep 'test-expired@example.com' /var/log/*.log 2>/dev/null || true")
		if strings.Contains(logs, "Charged") {
			t.Fatalf("Payment service charged credit card even though hold was expired!")
		}
	}
}
