package smoke

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Test01_LastItemRace verifies that when a product has stock=1,
// 50 concurrent virtual users attempting to add it to cart results in
// exactly 1 success, 49 failures, and Redis stock is never negative.
func Test01_LastItemRace(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	testProductID := getEnv("TEST_PRODUCT_ID", "OLJCESPC7Z") // Vintage Typewriter
	t.Logf("Running Last-Item Race test against %s for product %s", baseURL, testProductID)

	// If running with cluster access, ensure product stock is set to 1 in Redis for the test
	if isClusterAccessible() {
		_, _ = execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 SET 'stock:{%s}' 1", testProductID))
	}

	const concurrentUsers = 50
	var successCount int64
	var failureCount int64

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	for i := 0; i < concurrentUsers; i++ {
		wg.Add(1)
		go func(userIndex int) {
			defer wg.Done()

			client := createHTTPClient()
			// First fetch home page to establish session/cookie
			reqHome, err := http.NewRequest("GET", baseURL+"/", nil)
			if err != nil {
				atomic.AddInt64(&failureCount, 1)
				return
			}
			respHome, err := client.Do(reqHome)
			if err == nil {
				io.Copy(io.Discard, respHome.Body)
				respHome.Body.Close()
			}

			// Wait for synchronized start
			<-startBarrier

			// Concurrently attempt to add the item to cart
			formData := url.Values{
				"product_id": {testProductID},
				"quantity":   {"1"},
			}
			req, err := http.NewRequest("POST", baseURL+"/cart", strings.NewReader(formData.Encode()))
			if err != nil {
				atomic.AddInt64(&failureCount, 1)
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// Each virtual user sends distinct client IP so rate limiter distinguishes distinct users
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.1.%d", userIndex+1))
			req.Header.Set("X-Bypass-Rate-Limit", "test-suite")

			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&failureCount, 1)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			// FAIL FAST: Any HTTP 429 rate limit is treated as a hard failure
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Errorf("FAIL: User %d tripped HTTP 429 Too Many Requests during race test", userIndex)
				atomic.AddInt64(&failureCount, 1)
				return
			}

			// Success is typically 200 OK or 302/303 redirect with item present in cart
			bodyStr := string(body)
			if resp.StatusCode == http.StatusOK && !strings.Contains(bodyStr, "out of stock") && !strings.Contains(bodyStr, "not enough stock") {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failureCount, 1)
			}
		}(i)
	}

	// Release all 50 virtual users at the exact same instant
	close(startBarrier)
	wg.Wait()

	t.Logf("Race Results: %d succeeded, %d failed out of %d concurrent requests", successCount, failureCount, concurrentUsers)

	// Verification of acceptance criteria:
	// 1. Exactly 1 user succeeded in securing the last unit
	if successCount != 1 {
		t.Errorf("Expected exactly 1 user to reserve the last item, but got %d (failures: %d)", successCount, failureCount)
	}

	// 2. Redis stock is never negative
	if isClusterAccessible() {
		out, err := execInPod("online-boutique", "app=inventoryservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 GET 'stock:{%s}'", testProductID))
		if err == nil {
			t.Logf("Redis stock:{%s} after race: %s", testProductID, out)
			if strings.HasPrefix(out, "-") {
				t.Fatalf("Redis stock is negative: %s", out)
			}
		}
	}
}
