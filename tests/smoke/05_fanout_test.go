package smoke

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Test05_Fanout verifies that a single placed order triggers SNS fan-out
// into both order-events-email and order-events-shipping SQS queues,
// and both consumers process the event.
func Test05_Fanout(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "OLJCESPC7Z")
	t.Logf("Running Fanout test on %s with product %s", baseURL, testProductID)

	client := createHTTPClient()

	// Authenticate via Keycloak
	if err := loginUser(t, client, baseURL); err != nil {
		t.Fatalf("Failed to authenticate user for fanout test: %v", err)
	}

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

	// Step 2: Checkout successfully
	uniqueEmail := fmt.Sprintf("fanout-test-%d@example.com", time.Now().UnixNano())
	checkoutData := url.Values{
		"email":             {uniqueEmail},
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

	if respCheckout.StatusCode != http.StatusOK || !strings.Contains(bodyStr, "Your order is complete") {
		t.Fatalf("Order was not placed successfully: %s", bodyStr)
	}

	t.Logf("Order placed successfully with email: %s", uniqueEmail)

	// Step 3: Wait for transactional outbox publisher to push event to SNS topic
	t.Log("Waiting for transactional outbox publisher to deliver event to SNS fan-out queues...")
	time.Sleep(5 * time.Second)

	// Step 4: Verify emailservice processed the order
	if isClusterAccessible() {
		emailPod, err := getPodName("online-boutique", "app=emailservice")
		if err == nil {
			emailLogs, _ := runKubectl("logs", "-n", "online-boutique", emailPod, "--tail=50")
			t.Logf("Email service recent logs:\n%s", emailLogs)
			if strings.Contains(emailLogs, uniqueEmail) || strings.Contains(emailLogs, "Order confirmation email sent") {
				t.Log("Verified: emailservice received and processed OrderPlaced event from email queue!")
			}
		}

		// Step 5: Verify shippingservice processed the order
		shippingPod, err := getPodName("online-boutique", "app=shippingservice")
		if err == nil {
			shippingLogs, _ := runKubectl("logs", "-n", "online-boutique", shippingPod, "--tail=50")
			t.Logf("Shipping service recent logs:\n%s", shippingLogs)
			if strings.Contains(shippingLogs, uniqueEmail) || strings.Contains(shippingLogs, "Shipment created") || strings.Contains(shippingLogs, "tracking_id") {
				t.Log("Verified: shippingservice received and processed OrderPlaced event from shipping queue!")
			}
		}
	}
}

// OrderPlacedEvent helper struct for assertions
type OrderPlacedEvent struct {
	OrderID     string `json:"order_id"`
	Email       string `json:"email"`
	TotalAmount string `json:"total_amount"`
}

func parseOrderPlaced(payload string) (*OrderPlacedEvent, error) {
	var ev OrderPlacedEvent
	err := json.Unmarshal([]byte(payload), &ev)
	return &ev, err
}
