package smoke

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func getRedisClient() *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "redis:6379"
	}
	password := os.Getenv("REDIS_PASSWORD")
	if password == "" {
		password = os.Getenv("REDIS_AUTH")
	}
	var tlsConfig *tls.Config
	if strings.ToLower(os.Getenv("REDIS_TLS")) == "true" {
		insecure := strings.ToLower(os.Getenv("REDIS_TLS_INSECURE")) == "true"
		tlsConfig = &tls.Config{InsecureSkipVerify: insecure}
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:      addr,
		Password:  password,
		TLSConfig: tlsConfig,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil
	}
	return rdb
}

// loginUser executes the complete Keycloak OAuth2 / OIDC authorization code flow
// establishing the authenticated session cookie on the client.
func loginUser(t *testing.T, client *http.Client, baseURL string) error {
	username := os.Getenv("KEYCLOAK_TEST_USER")
	if username == "" {
		username = "testuser"
	}
	password := os.Getenv("KEYCLOAK_TEST_PASSWORD")
	if password == "" {
		password = "TestPass123!"
	}

	noRedirectClient := &http.Client{
		Jar:     client.Jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Initiate login
	loginInitResp, err := noRedirectClient.Get(baseURL + "/login?redirect=/cart")
	if err != nil {
		return fmt.Errorf("failed to initiate login: %w", err)
	}
	defer loginInitResp.Body.Close()

	keycloakAuthURL := loginInitResp.Header.Get("Location")
	if keycloakAuthURL == "" {
		return fmt.Errorf("no Location header in /login response (status: %d)", loginInitResp.StatusCode)
	}

	// 2. Fetch login page
	loginPageResp, err := client.Get(keycloakAuthURL)
	if err != nil {
		return fmt.Errorf("failed to fetch Keycloak login page at %s: %w", keycloakAuthURL, err)
	}
	defer loginPageResp.Body.Close()

	pageBytes, _ := io.ReadAll(loginPageResp.Body)
	pageStr := string(pageBytes)

	re := regexp.MustCompile(`action="([^"]+)"`)
	matches := re.FindStringSubmatch(pageStr)
	if len(matches) < 2 {
		return fmt.Errorf("login form action URL not found in Keycloak HTML response: %s", pageStr)
	}
	actionURL := strings.ReplaceAll(matches[1], "&amp;", "&")

	// 3. Submit credentials
	formData := url.Values{
		"username": {username},
		"password": {password},
	}
	authResp, err := noRedirectClient.PostForm(actionURL, formData)
	if err != nil {
		return fmt.Errorf("failed to submit credentials to Keycloak: %w", err)
	}
	defer authResp.Body.Close()

	if authResp.StatusCode != http.StatusFound && authResp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(authResp.Body)
		return fmt.Errorf("Keycloak credentials submission failed (status %d): %s", authResp.StatusCode, string(body))
	}

	callbackURL := authResp.Header.Get("Location")
	if callbackURL == "" {
		return fmt.Errorf("no Location header after Keycloak credentials submission")
	}

	// 4. Follow callback
	callbackResp, err := client.Get(callbackURL)
	if err != nil {
		return fmt.Errorf("failed to complete callback exchange: %w", err)
	}
	defer callbackResp.Body.Close()

	// Verify session cookie
	u, _ := url.Parse(baseURL)
	cookies := client.Jar.Cookies(u)
	hasAuth := false
	for _, c := range cookies {
		if c.Name == "boutique_auth_session" && c.Value != "" {
			hasAuth = true
			break
		}
	}
	if !hasAuth {
		return fmt.Errorf("boutique_auth_session cookie not found after login callback")
	}

	// 5. Empty any leftover items in user's cart so tests start with a clean slate
	emptyCart(client, baseURL)

	t.Logf("User %s authenticated successfully via Keycloak OIDC flow", username)
	return nil
}

func emptyCart(client *http.Client, baseURL string) {
	req, _ := http.NewRequest("POST", baseURL+"/cart/empty", nil)
	resp, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getBaseURL() string {
	baseURL := os.Getenv("BASE_URL")
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}

	// Try to query ingress-nginx ALB hostname via kubectl
	out, err := exec.Command("kubectl", "get", "svc", "ingress-nginx-controller", "-n", "ingress-nginx",
		"-o", "jsonpath={.status.loadBalancer.ingress[0].hostname}").Output()
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return "http://" + strings.TrimSpace(string(out))
	}

	return "http://localhost:8080"
}

func getClusterName() string {
	if name := os.Getenv("CLUSTER_NAME"); name != "" {
		return name
	}
	return "online-boutique-dev"
}

func getAWSRegion() string {
	if region := os.Getenv("AWS_REGION"); region != "" {
		return region
	}
	return "il-central-1"
}

func runKubectl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s failed: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func runAWS(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fullArgs := append([]string{"--region", getAWSRegion()}, args...)
	cmd := exec.CommandContext(ctx, "aws", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("aws %s failed: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func isClusterAccessible() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "version", "--client")
	if err := cmd.Run(); err != nil {
		return false
	}
	cmdCheck := exec.CommandContext(ctx, "kubectl", "get", "nodes")
	return cmdCheck.Run() == nil
}

func isURLAccessible(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func getPodName(namespace, selector string) (string, error) {
	out, err := runKubectl("get", "pods", "-n", namespace, "-l", selector,
		"--field-selector=status.phase=Running", "-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", fmt.Errorf("no running pod found in %s with selector %s", namespace, selector)
	}
	return name, nil
}

func execInPod(namespace, selector string, cmd ...string) (string, error) {
	pod, err := getPodName(namespace, selector)
	if err != nil {
		return "", err
	}
	args := append([]string{"exec", "-n", namespace, pod, "--"}, cmd...)
	return runKubectl(args...)
}

func createHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Follow redirects
		},
	}
}

func parsePrometheusMetric(metricsBody, metricName string) (float64, error) {
	lines := strings.Split(metricsBody, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, metricName) {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseFloat(fields[len(fields)-1], 64)
			}
		}
	}
	return 0, fmt.Errorf("metric %s not found", metricName)
}

func requireCluster(t *testing.T) {
	if !isClusterAccessible() {
		t.Skip("Skipping test: Kubernetes cluster is not accessible from current environment")
	}
}

func requireURL(t *testing.T, url string) {
	if !isURLAccessible(url) {
		t.Skipf("Skipping test: URL %s is not reachable", url)
	}
}
