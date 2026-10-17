#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/docker-compose.e2e.yml"

echo "=========================================================="
echo "ONLINE BOUTIQUE: LOCAL END-TO-END VERIFICATION (DOCKER COMPOSE)"
echo "=========================================================="
echo "Workspace: ${ROOT_DIR}"
echo "Compose File: ${COMPOSE_FILE}"

# 1. Bring up stack
echo ""
echo "=== Step 1: Bringing up Docker Compose Stack ==="
docker compose -f "${COMPOSE_FILE}" up -d

# 2. Wait for Health
echo ""
echo "=== Step 2: Waiting for Services Health & Readiness ==="

echo "Waiting for PostgreSQL to be healthy..."
until [ "$(docker inspect --format='{{.State.Health.Status}}' e2e-postgres 2>/dev/null)" = "healthy" ]; do
  sleep 2
done
echo "  [OK] PostgreSQL is healthy!"

echo "Waiting for db-init schema migration to finish..."
docker wait e2e-db-init >/dev/null 2>&1 || true
INIT_EXIT=$(docker inspect --format='{{.State.ExitCode}}' e2e-db-init 2>/dev/null || echo "1")
if [ "${INIT_EXIT}" != "0" ]; then
  echo "  [FAIL] e2e-db-init failed with exit code ${INIT_EXIT}!"
  docker logs e2e-db-init
  exit 1
fi
echo "  [OK] Database schema & seed migration completed successfully!"

echo "Waiting for Redis to be healthy..."
until [ "$(docker inspect --format='{{.State.Health.Status}}' e2e-redis 2>/dev/null)" = "healthy" ]; do
  sleep 2
done
echo "  [OK] Redis is healthy!"

echo "Waiting for LocalStack to be healthy..."
until [ "$(docker inspect --format='{{.State.Health.Status}}' e2e-localstack 2>/dev/null)" = "healthy" ]; do
  sleep 2
done
echo "  [OK] LocalStack is healthy!"

echo "Waiting for LocalStack SNS/SQS init script..."
for i in $(seq 1 20); do
  if docker exec e2e-localstack awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-email >/dev/null 2>&1; then
    echo "  [OK] LocalStack SQS queues & SNS topic verified!"
    break
  fi
  sleep 2
done

echo "Waiting for Keycloak to be ready (http://localhost:8082/auth)..."
for i in $(seq 1 30); do
  if curl -s -f "http://localhost:8082/auth/realms/online-boutique" >/dev/null 2>&1; then
    echo "  [OK] Keycloak online-boutique realm is ready!"
    break
  fi
  sleep 3
done

echo "Waiting for Boutique Frontend to be ready (http://localhost:8080)..."
for i in $(seq 1 30); do
  if curl -s -f "http://localhost:8080/" >/dev/null 2>&1; then
    echo "  [OK] Frontend is ready and serving HTTP traffic!"
    break
  fi
  sleep 2
done

# 3. Snapshot initial state
echo ""
echo "=== Step 3: Snapshot Initial Database State ==="
INITIAL_STOCK=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT total_stock FROM inventory WHERE product_id = 'OLJCESPC7Z';")
INITIAL_ORDERS_COUNT=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM orders;")
INITIAL_OUTBOX_COUNT=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM outbox;")
echo "  Initial Sunglasses stock: ${INITIAL_STOCK}"
echo "  Initial Orders count:     ${INITIAL_ORDERS_COUNT}"
echo "  Initial Outbox count:     ${INITIAL_OUTBOX_COUNT}"
echo "  [DB State Before Run] Orders by status:"
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT status, count(*) FROM orders GROUP BY status;"
echo "  [DB State Before Run] Unpublished outbox events:"
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT count(*) FROM outbox WHERE published_at IS NULL;"

# 4. Run Scripted User Flow
echo ""
echo "=== Step 4: Running Scripted User Flow (Python / HTTP) ==="
PYTHON_CMD="python3"
if ! command -v python3 >/dev/null 2>&1; then
  if command -v python >/dev/null 2>&1; then
    PYTHON_CMD="python"
  elif command -v py >/dev/null 2>&1; then
    PYTHON_CMD="py"
  fi
fi
$PYTHON_CMD "${SCRIPT_DIR}/e2e_user_flow.py"

# 5. Wait for Asynchronous Event Fanout & Processing
echo ""
echo "=== Step 5: Awaiting Asynchronous Outbox Worker & Consumer Polling (6s) ==="
sleep 6

# 6. Assertions
echo ""
echo "=== Step 6: Verifying End-to-End System Invariants & Assertions ==="

FAILED=0

# Assertion A: Postgres orders table
echo -n "Checking Assertion 1 (Postgres orders row CONFIRMED)... "
ORDER_ROW=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT order_id, email, status FROM orders WHERE email = 'testuser@example.com' ORDER BY created_at DESC LIMIT 1;")
SPECIFIC_ORDER_ID=""
if [[ "${ORDER_ROW}" == *"CONFIRMED"* ]]; then
  SPECIFIC_ORDER_ID=$(echo "${ORDER_ROW}" | cut -d'|' -f1)
  echo "[PASS] Found order: ${ORDER_ROW} (ID: ${SPECIFIC_ORDER_ID})"
else
  echo "[FAIL] Expected CONFIRMED order row in Postgres, found: '${ORDER_ROW}'"
  FAILED=1
fi

# Assertion B: Inventory decreased
echo -n "Checking Assertion 2 (Inventory total_stock decreased)... "
FINAL_STOCK=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT total_stock FROM inventory WHERE product_id = 'OLJCESPC7Z';")
EXPECTED_STOCK=$((INITIAL_STOCK - 1))
if [ "${FINAL_STOCK}" -eq "${EXPECTED_STOCK}" ]; then
  echo "[PASS] Stock decreased from ${INITIAL_STOCK} to ${FINAL_STOCK} (as expected)"
else
  echo "[FAIL] Expected stock ${EXPECTED_STOCK}, got ${FINAL_STOCK}"
  FAILED=1
fi

# Assertion C: Outbox published for THIS SPECIFIC order_id
echo -n "Checking Assertion 3 (Outbox event for order ${SPECIFIC_ORDER_ID} published_at is set)... "
SPECIFIC_OUTBOX=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM outbox WHERE id = '${SPECIFIC_ORDER_ID}' AND published_at IS NOT NULL;")
if [ "${SPECIFIC_OUTBOX}" -eq 1 ]; then
  OUTBOX_ROW=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT id, event_type, published_at FROM outbox WHERE id = '${SPECIFIC_ORDER_ID}';")
  echo "[PASS] Exact outbox event published for order ${SPECIFIC_ORDER_ID} (${OUTBOX_ROW})"
else
  ALL_OUTBOX_ROWS=$(docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT id, event_type, published_at FROM outbox WHERE id = '${SPECIFIC_ORDER_ID}';")
  echo "[FAIL] Expected exactly 1 published outbox event for order ${SPECIFIC_ORDER_ID}, found: '${ALL_OUTBOX_ROWS}'"
  FAILED=1
fi

# Assertion D: Email consumer processed message
echo -n "Checking Assertion 4a (Email consumer processed order event)... "
if docker logs e2e-emailservice 2>&1 | grep -qE "Order confirmation email|Processed order event"; then
  EMAIL_LOG=$(docker logs e2e-emailservice 2>&1 | grep -E "Order confirmation email|Processed order event" | tail -n 1)
  echo "[PASS] ${EMAIL_LOG}"
else
  echo "[FAIL] No processed order confirmation log found in emailservice!"
  FAILED=1
fi

# Assertion E: Shipping consumer processed message
echo -n "Checking Assertion 4b (Shipping consumer processed order event)... "
if docker logs e2e-shippingservice 2>&1 | grep -qE "Processed shipping order event|Tracking ID"; then
  SHIPPING_LOG=$(docker logs e2e-shippingservice 2>&1 | grep -E "Processed shipping order event|Tracking ID" | tail -n 1)
  echo "[PASS] ${SHIPPING_LOG}"
else
  echo "[FAIL] No processed shipping order event found in shippingservice!"
  FAILED=1
fi

# Assertion F: LocalStack SQS main queues and DLQs empty
echo -n "Checking Assertion 5a (email queue has 0 messages left)... "
EMAIL_Q_MSGS=$(docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-email" --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --query "Attributes.ApproximateNumberOfMessages" --output text 2>/dev/null || echo "0")
if [ "${EMAIL_Q_MSGS}" -eq 0 ]; then
  echo "[PASS] 0 messages in email queue"
else
  echo "[FAIL] ${EMAIL_Q_MSGS} messages still in email queue"
  FAILED=1
fi

echo -n "Checking Assertion 5b (shipping queue has 0 messages left)... "
SHIPPING_Q_MSGS=$(docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-shipping" --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible --query "Attributes.ApproximateNumberOfMessages" --output text 2>/dev/null || echo "0")
if [ "${SHIPPING_Q_MSGS}" -eq 0 ]; then
  echo "[PASS] 0 messages in shipping queue"
else
  echo "[FAIL] ${SHIPPING_Q_MSGS} messages still in shipping queue"
  FAILED=1
fi

echo -n "Checking Assertion 5c (DLQs have 0 messages)... "
EMAIL_DLQ_MSGS=$(docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-email-dlq" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text 2>/dev/null || echo "0")
SHIPPING_DLQ_MSGS=$(docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-shipping-dlq" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text 2>/dev/null || echo "0")

if [ "${EMAIL_DLQ_MSGS}" -eq 0 ] && [ "${SHIPPING_DLQ_MSGS}" -eq 0 ]; then
  echo "[PASS] 0 messages in email DLQ and 0 messages in shipping DLQ"
else
  echo "[FAIL] Messages found in DLQs! Email DLQ: ${EMAIL_DLQ_MSGS}, Shipping DLQ: ${SHIPPING_DLQ_MSGS}"
  FAILED=1
fi

echo ""
echo "=== Post-Run Database State Verification ==="
echo "  [DB State After Run] Orders by status:"
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT status, count(*) FROM orders GROUP BY status;"
echo "  [DB State After Run] Unpublished outbox events:"
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT count(*) FROM outbox WHERE published_at IS NULL;"

echo ""
echo "=========================================================="
if [ "${FAILED}" -eq 0 ]; then
  echo "ALL END-TO-END ASSERTIONS PASSED! STACK VERIFIED LOCALLY."
  echo "=========================================================="
  exit 0
else
  echo "SOME ASSERTIONS FAILED. CHECK LOGS ABOVE."
  echo "=========================================================="
  exit 1
fi
