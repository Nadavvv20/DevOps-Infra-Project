package smoke

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
