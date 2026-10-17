#!/bin/sh
set -e

# Forward localhost:8080 -> frontend:8080 and localhost:8082 -> keycloak:8080
socat TCP-LISTEN:8080,fork,reuseaddr TCP:frontend:8080 &
SOCAT_PID1=$!
socat TCP-LISTEN:8082,fork,reuseaddr TCP:keycloak:8080 &
SOCAT_PID2=$!

trap "kill $SOCAT_PID1 $SOCAT_PID2 2>/dev/null || true" EXIT

sleep 1

export BASE_URL="http://localhost:8080"
export KEYCLOAK_TEST_USER="testuser"
export KEYCLOAK_TEST_PASSWORD="TestPass123!"
export REDIS_ADDR="redis:6379"
export REDIS_PASSWORD="boutique_redis_pass"
export REDIS_TLS="true"
export REDIS_TLS_INSECURE="true"

cd "/repos/DevOps Infra Project/tests/smoke"
go test -v -run "Test01|Test02|Test04|Test05|Test09|Test10|Test11|Test13" .
