#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Smoke Test Runner — Online Boutique Architecture Validation Suite
# Runs the 13 verification tests against the deployed cluster/ALB.
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

echo "Target Configuration:"
echo "  Base URL:     ${BASE_URL}"
echo "  AWS Region:   ${AWS_REGION}"
echo "  Cluster Name: ${CLUSTER_NAME}"
echo ""

cd "${REPO_ROOT}/tests/smoke"

if command -v go >/dev/null 2>&1; then
  echo "Executing Go smoke test suite..."
  go test -v -timeout 15m ./...
elif command -v docker >/dev/null 2>&1; then
  echo "Go not installed locally, executing via official golang container..."
  docker run --rm \
    --network host \
    -e BASE_URL="${BASE_URL}" \
    -e AWS_REGION="${AWS_REGION}" \
    -e CLUSTER_NAME="${CLUSTER_NAME}" \
    -v "${REPO_ROOT}/tests/smoke:/workspace" \
    -w /workspace \
    golang:1.23-alpine \
    go test -v -timeout 15m ./...
else
  echo "Error: Neither 'go' nor 'docker' is available to run tests."
  exit 1
fi

echo ""
echo "=========================================================="
echo " All Smoke Tests Finished Successfully!                   "
echo "=========================================================="
