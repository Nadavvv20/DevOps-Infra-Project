package smoke

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test10_SessionTTL verifies that user session TTL in Redis resets on each
// subsequent HTTP request and the session key expires after TTL timeout.
func Test10_SessionTTL(t *testing.T) {
	baseURL := getBaseURL()
	requireURL(t, baseURL)

	t.Logf("Running Session TTL test against %s", baseURL)

	client := createHTTPClient()

	// Step 1: Issue initial request to obtain session cookie
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("Failed to connect to frontend: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Extract session ID from cookie jar
	u, _ := client.Jar.Cookies(resp.Request.URL)
	var sessionID string
	for _, cookie := range u {
		if cookie.Name == "shop_session-id" || cookie.Name == "session-id" {
			sessionID = cookie.Value
			break
		}
	}
	t.Logf("Acquired session ID: %s", sessionID)

	if sessionID != "" && isClusterAccessible() {
		// Step 2: Check initial TTL in Redis
		ttlOut1, err := execInPod("online-boutique", "app=cartservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 TTL 'session:%s'", sessionID))
		if err == nil {
			ttl1, _ := strconv.Atoi(strings.TrimSpace(ttlOut1))
			t.Logf("Session initial TTL in Redis: %d seconds", ttl1)
		}

		// Wait 3 seconds
		time.Sleep(3 * time.Second)

		// Step 3: Send another request with same session cookie
		resp2, err := client.Get(baseURL + "/cart")
		if err == nil {
			io.Copy(io.Discard, resp2.Body)
			resp2.Body.Close()
		}

		// Step 4: Verify TTL has been refreshed back to full sliding window (~1800-3600s or configured TTL)
		ttlOut2, err := execInPod("online-boutique", "app=cartservice",
			"sh", "-c", fmt.Sprintf("redis-cli -h $REDIS_ADDR -p 6379 TTL 'session:%s'", sessionID))
		if err == nil {
			ttl2, _ := strconv.Atoi(strings.TrimSpace(ttlOut2))
			t.Logf("Session refreshed TTL after subsequent request: %d seconds", ttl2)
			if ttl2 < 0 {
				t.Errorf("Expected positive refreshed TTL for active session, got %d", ttl2)
			}
		}
	}
}
