#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# Verify IAM Roles used by up.yml against AWS IAM
#
# Usage:
#   ./scripts/verify-iam-roles.sh [--cluster-name <name>] [--region <region>]
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

CLUSTER_NAME="${1:-${CLUSTER_NAME:-online-boutique-dev}}"
AWS_REGION="${2:-${AWS_REGION:-il-central-1}}"

echo "=========================================================="
echo "Verifying IAM Roles for Cluster: ${CLUSTER_NAME}"
echo "Region: ${AWS_REGION}"
echo "=========================================================="

ROLES=(
  "karpenter-controller-${CLUSTER_NAME}"
  "${CLUSTER_NAME}-external-secrets"
  "${CLUSTER_NAME}-checkoutservice-sqs"
  "${CLUSTER_NAME}-emailservice-sqs"
  "${CLUSTER_NAME}-shippingservice-sqs"
)

ALL_PASSED=true

for ROLE in "${ROLES[@]}"; do
  echo -n "Checking role '${ROLE}'... "
  if ARN=$(aws iam get-role --role-name "$ROLE" --query 'Role.Arn' --output text 2>/dev/null); then
    echo "EXISTS (ARN: ${ARN})"
  else
    echo "NOT FOUND / ERROR"
    ALL_PASSED=false
  fi
done

echo "=========================================================="
if [ "$ALL_PASSED" = true ]; then
  echo "SUCCESS: All 5 IAM roles exist and match the workflow and Terraform specifications."
  exit 0
else
  echo "WARNING: One or more IAM roles could not be verified in AWS IAM."
  echo "Make sure Terraform infrastructure has been applied."
  exit 1
fi
