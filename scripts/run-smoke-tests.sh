#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Smoke Test Runner — Online Boutique Architecture Validation Suite
# Runs against the deployed cluster/ALB.
# ─────────────────────────────────────────────────────────────────────────────

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=========================================================="
echo " Starting Online Boutique Architecture Smoke Test Suite   "
echo "=========================================================="

if [ -z "$BASE_URL" ]; then
  if command -v kubectl >/dev/null 2>&1; then
    ALB_HOST=$(kubectl get svc ingress-nginx-controller -n ingress-nginx -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)
    if [ -n "$ALB_HOST" ]; then
      export BASE_URL="http://${ALB_HOST}"
    fi
  fi
fi

export BASE_URL="${BASE_URL:-http://localhost:8080}"
export AWS_REGION="${AWS_REGION:-il-central-1}"
export CLUSTER_NAME="${CLUSTER_NAME:-online-boutique-dev}"
export RUN_DESTRUCTIVE="${RUN_DESTRUCTIVE:-false}"

echo "Target Configuration:"
echo "  Base URL:        ${BASE_URL}"
echo "  AWS Region:      ${AWS_REGION}"
echo "  Cluster Name:    ${CLUSTER_NAME}"
echo "  Run Destructive: ${RUN_DESTRUCTIVE}"
echo ""

cd "${REPO_ROOT}/tests/smoke"

TEST_PATTERN="."
if [ "$RUN_DESTRUCTIVE" != "true" ]; then
  echo "Running FAST non-destructive smoke test suite (safe after every deploy)..."
  TEST_PATTERN="Test01|Test04|Test05|Test07|Test09|Test10|Test11|Test12|Test13"
else
  echo "Running FULL test suite including DESTRUCTIVE tests (pod kills, DLQ injection)..."
fi

if command -v go >/dev/null 2>&1; then
  echo "Executing Go smoke test suite..."
  go test -v -timeout 15m -run "${TEST_PATTERN}" ./...
elif command -v docker >/dev/null 2>&1; then
  echo "Executing via official golang container..."
  docker run --rm \
    --network host \
    -e BASE_URL="${BASE_URL}" \
    -e AWS_REGION="${AWS_REGION}" \
    -e CLUSTER_NAME="${CLUSTER_NAME}" \
    -v "${REPO_ROOT}/tests/smoke:/workspace" \
    -w /workspace \
    golang:1.23-alpine \
    go test -v -timeout 15m -run "${TEST_PATTERN}" ./...
else
  echo "Executing via WSL Go..."
  wsl -d Ubuntu -- bash -c "cd '${REPO_ROOT}/tests/smoke' && ~/go/bin/go test -v -timeout 15m -run '${TEST_PATTERN}' ./..."
fi

echo ""
echo "=========================================================="
echo " Smoke Tests Completed!                                   "
echo "=========================================================="
