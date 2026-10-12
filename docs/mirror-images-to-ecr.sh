#!/usr/bin/env bash
# =============================================================================
# mirror-images-to-ecr.sh
# Script to easily enable Option B: Mirror upstream Google images to private ECR.
# =============================================================================
set -euo pipefail

AWS_REGION="${1:-il-central-1}"
AWS_ACCOUNT_ID="${2:-965085140318}"
ECR_REGISTRY="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
TAG="v0.10.1"

echo "Logging in to Amazon ECR: ${ECR_REGISTRY}..."
aws ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${ECR_REGISTRY}"

SERVICES=(
  "frontend"
  "cartservice"
  "productcatalogservice"
  "currencyservice"
  "paymentservice"
  "shippingservice"
  "emailservice"
  "checkoutservice"
  "recommendationservice"
  "adservice"
  "loadgenerator"
)

for svc in "${SERVICES[@]}"; do
  echo "===> Pulling gcr.io/google-samples/microservices-demo/${svc}:${TAG}..."
  docker pull "gcr.io/google-samples/microservices-demo/${svc}:${TAG}"

  echo "===> Tagging for ECR..."
  docker tag "gcr.io/google-samples/microservices-demo/${svc}:${TAG}" "${ECR_REGISTRY}/online-boutique/${svc}:latest"

  echo "===> Pushing to ${ECR_REGISTRY}/online-boutique/${svc}:latest..."
  docker push "${ECR_REGISTRY}/online-boutique/${svc}:latest"
done

echo "All 11 microservice images successfully mirrored to private ECR!"
