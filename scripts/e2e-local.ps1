# ==========================================================
# ONLINE BOUTIQUE: LOCAL END-TO-END VERIFICATION (POWERSHELL)
# ==========================================================
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RootDir = Split-Path -Parent $ScriptDir
$ComposeFile = Join-Path $RootDir "docker-compose.e2e.yml"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "ONLINE BOUTIQUE: LOCAL END-TO-END VERIFICATION (DOCKER COMPOSE)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Workspace: $RootDir"
Write-Host "Compose File: $ComposeFile"

# 1. Bring up stack
Write-Host "`n=== Step 1: Bringing up Docker Compose Stack ===" -ForegroundColor Yellow
docker compose -f $ComposeFile up -d

# 2. Wait for Health
Write-Host "`n=== Step 2: Waiting for Services Health & Readiness ===" -ForegroundColor Yellow
$prevEAP = $ErrorActionPreference
$ErrorActionPreference = "Continue"

Write-Host "Waiting for PostgreSQL to be healthy..."
while ((docker inspect --format='{{.State.Health.Status}}' e2e-postgres 2>$null) -ne "healthy") {
    Start-Sleep -Seconds 2
}
Write-Host "  [OK] PostgreSQL is healthy!" -ForegroundColor Green

Write-Host "Waiting for db-init schema migration to finish..."
docker wait e2e-db-init > $null 2>&1
$InitExit = (docker inspect --format='{{.State.ExitCode}}' e2e-db-init 2>$null)
if ($InitExit -ne "0") {
    Write-Host "  [FAIL] e2e-db-init failed with exit code $InitExit!" -ForegroundColor Red
    docker logs e2e-db-init
    exit 1
}
Write-Host "  [OK] Database schema & seed migration completed successfully!" -ForegroundColor Green

Write-Host "Waiting for Redis to be healthy..."
while ((docker inspect --format='{{.State.Health.Status}}' e2e-redis 2>$null) -ne "healthy") {
    Start-Sleep -Seconds 2
}
Write-Host "  [OK] Redis is healthy!" -ForegroundColor Green

Write-Host "Waiting for LocalStack to be healthy..."
while ((docker inspect --format='{{.State.Health.Status}}' e2e-localstack 2>$null) -ne "healthy") {
    Start-Sleep -Seconds 2
}
Write-Host "  [OK] LocalStack is healthy!" -ForegroundColor Green

Write-Host "Waiting for LocalStack SNS/SQS init script..."
for ($i = 1; $i -le 20; $i++) {
    $res = cmd /c "docker exec e2e-localstack awslocal sqs get-queue-url --queue-name online-boutique-dev-order-events-email 2>nul"
    if ($LASTEXITCODE -eq 0) {
        Write-Host "  [OK] LocalStack SQS queues & SNS topic verified!" -ForegroundColor Green
        break
    }
    Start-Sleep -Seconds 2
}

Write-Host "Waiting for Keycloak to be ready (http://localhost:8082/auth)..."
for ($i = 1; $i -le 30; $i++) {
    try {
        $resp = Invoke-WebRequest -Uri "http://localhost:8082/auth/realms/online-boutique" -UseBasicParsing -TimeoutSec 3 -ErrorAction SilentlyContinue
        if ($resp.StatusCode -eq 200) {
            Write-Host "  [OK] Keycloak online-boutique realm is ready!" -ForegroundColor Green
            break
        }
    } catch {
        # continue waiting
    }
    Start-Sleep -Seconds 2
}

Write-Host "Waiting for Boutique Frontend to be ready (http://localhost:8080)..."
for ($i = 1; $i -le 30; $i++) {
    try {
        $resp = Invoke-WebRequest -Uri "http://localhost:8080/" -UseBasicParsing -TimeoutSec 3 -ErrorAction SilentlyContinue
        if ($resp.StatusCode -eq 200) {
            Write-Host "  [OK] Frontend is ready and serving HTTP traffic!" -ForegroundColor Green
            break
        }
    } catch {
        # continue waiting
    }
    Start-Sleep -Seconds 2
}
$ErrorActionPreference = $prevEAP

# 3. Snapshot initial state
Write-Host "`n=== Step 3: Snapshot Initial Database State ===" -ForegroundColor Yellow
$InitialStock = [int](docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT total_stock FROM inventory WHERE product_id = 'OLJCESPC7Z';")
$InitialOrders = [int](docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM orders;")
$InitialOutbox = [int](docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM outbox WHERE event_type = 'OrderPlaced' AND published_at IS NOT NULL;")
Write-Host "  Initial Sunglasses stock: $InitialStock"
Write-Host "  Initial Orders count:     $InitialOrders"
Write-Host "  Initial Published Outbox: $InitialOutbox"
Write-Host "  [DB State Before Run] Orders by status:" -ForegroundColor Cyan
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT status, count(*) FROM orders GROUP BY status;"
Write-Host "  [DB State Before Run] Unpublished outbox events:" -ForegroundColor Cyan
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT count(*) FROM outbox WHERE published_at IS NULL;"

# 4. Run Scripted User Flow
Write-Host "`n=== Step 4: Running Scripted User Flow (Python / HTTP) ===" -ForegroundColor Yellow
$UserFlowScript = Join-Path $ScriptDir "e2e_user_flow.py"
py $UserFlowScript
if ($LASTEXITCODE -ne 0) {
    Write-Host "User flow failed with exit code $LASTEXITCODE!" -ForegroundColor Red
    exit 1
}

# 5. Wait for Asynchronous Event Fanout & Processing
Write-Host "`n=== Step 5: Awaiting Asynchronous Outbox Worker & Consumer Polling (6s) ===" -ForegroundColor Yellow
Start-Sleep -Seconds 6

# 6. Assertions
Write-Host "`n=== Step 6: Verifying End-to-End System Invariants & Assertions ===" -ForegroundColor Yellow
$Failed = 0

# Assertion 1: Postgres orders table
Write-Host -NoNewline "Checking Assertion 1 (Postgres orders row CONFIRMED)... "
$OrderRow = docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT order_id, email, status FROM orders WHERE email = 'testuser@example.com' ORDER BY created_at DESC LIMIT 1;"
$SpecificOrderID = ""
if ($OrderRow -match "CONFIRMED") {
    $SpecificOrderID = ($OrderRow -split '\|')[0]
    Write-Host "[PASS] Found order: $OrderRow (ID: $SpecificOrderID)" -ForegroundColor Green
} else {
    Write-Host "[FAIL] Expected CONFIRMED order row in Postgres, found: '$OrderRow'" -ForegroundColor Red
    $Failed = 1
}

# Assertion 2: Inventory stock decreased
Write-Host -NoNewline "Checking Assertion 2 (Inventory total_stock decreased)... "
$FinalStock = [int](docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT total_stock FROM inventory WHERE product_id = 'OLJCESPC7Z';")
$ExpectedStock = $InitialStock - 1
if ($FinalStock -eq $ExpectedStock) {
    Write-Host "[PASS] Stock decreased from $InitialStock to $FinalStock" -ForegroundColor Green
} else {
    Write-Host "[FAIL] Expected stock $ExpectedStock, got $FinalStock" -ForegroundColor Red
    $Failed = 1
}

# Assertion 3: Outbox published for THIS SPECIFIC order_id
Write-Host -NoNewline "Checking Assertion 3 (Outbox event for order $SpecificOrderID published_at is set)... "
$SpecificOutbox = [int](docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT count(*) FROM outbox WHERE id = '$SpecificOrderID' AND published_at IS NOT NULL;")
if ($SpecificOutbox -eq 1) {
    $OutboxRow = docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT id, event_type, published_at FROM outbox WHERE id = '$SpecificOrderID';"
    Write-Host "[PASS] Exact outbox event published for order $SpecificOrderID ($OutboxRow)" -ForegroundColor Green
} else {
    $AllOutboxRows = docker exec e2e-postgres psql -U boutique_user -d boutique_db -t -A -c "SELECT id, event_type, published_at FROM outbox WHERE id = '$SpecificOrderID';"
    Write-Host "[FAIL] Expected exactly 1 published outbox event for order $SpecificOrderID, found: '$AllOutboxRows'" -ForegroundColor Red
    $Failed = 1
}

# Assertion 4a: Email consumer
Write-Host -NoNewline "Checking Assertion 4a (Email consumer processed order event)... "
$EmailLogs = docker logs e2e-emailservice 2>&1
$EmailMatch = $EmailLogs | Select-String "Order confirmation email successfully dispatched" | Select-Object -Last 1
if ($EmailMatch) {
    Write-Host "[PASS] $($EmailMatch.Line.Trim())" -ForegroundColor Green
} else {
    Write-Host "[FAIL] No processed order confirmation log found in emailservice!" -ForegroundColor Red
    $Failed = 1
}

# Assertion 4b: Shipping consumer
Write-Host -NoNewline "Checking Assertion 4b (Shipping consumer processed order event)... "
$ShippingLogs = docker logs e2e-shippingservice 2>&1
$ShippingMatch = $ShippingLogs | Select-String "Successfully processed shipment for Order" | Select-Object -Last 1
if ($ShippingMatch) {
    Write-Host "[PASS] $($ShippingMatch.Line.Trim())" -ForegroundColor Green
} else {
    Write-Host "[FAIL] No processed shipping order event found in shippingservice!" -ForegroundColor Red
    $Failed = 1
}

# Assertion 5: SQS Queues and DLQs
Write-Host -NoNewline "Checking Assertion 5a (email queue has 0 messages left)... "
$EmailQ = docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-email" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text
if ([int]$EmailQ -eq 0) {
    Write-Host "[PASS] 0 messages in email queue" -ForegroundColor Green
} else {
    Write-Host "[FAIL] $EmailQ messages still in email queue" -ForegroundColor Red
    $Failed = 1
}

Write-Host -NoNewline "Checking Assertion 5b (shipping queue has 0 messages left)... "
$ShippingQ = docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-shipping" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text
if ([int]$ShippingQ -eq 0) {
    Write-Host "[PASS] 0 messages in shipping queue" -ForegroundColor Green
} else {
    Write-Host "[FAIL] $ShippingQ messages still in shipping queue" -ForegroundColor Red
    $Failed = 1
}

Write-Host -NoNewline "Checking Assertion 5c (DLQs have 0 messages)... "
$EmailDLQ = docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-email-dlq" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text
$ShippingDLQ = docker exec e2e-localstack awslocal sqs get-queue-attributes --queue-url "http://sqs.il-central-1.localhost.localstack.cloud:4566/000000000000/online-boutique-dev-order-events-shipping-dlq" --attribute-names ApproximateNumberOfMessages --query "Attributes.ApproximateNumberOfMessages" --output text

if (([int]$EmailDLQ -eq 0) -and ([int]$ShippingDLQ -eq 0)) {
    Write-Host "[PASS] 0 messages in email DLQ and 0 messages in shipping DLQ" -ForegroundColor Green
} else {
    Write-Host "[FAIL] Messages found in DLQs! Email DLQ: $EmailDLQ, Shipping DLQ: $ShippingDLQ" -ForegroundColor Red
    $Failed = 1
}

Write-Host "`n=== Post-Run Database State Verification ===" -ForegroundColor Yellow
Write-Host "  [DB State After Run] Orders by status:" -ForegroundColor Cyan
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT status, count(*) FROM orders GROUP BY status;"
Write-Host "  [DB State After Run] Unpublished outbox events:" -ForegroundColor Cyan
docker exec e2e-postgres psql -U boutique_user -d boutique_db -c "SELECT count(*) FROM outbox WHERE published_at IS NULL;"

Write-Host "`n==========================================================" -ForegroundColor Cyan
if ($Failed -eq 0) {
    Write-Host "ALL END-TO-END ASSERTIONS PASSED! STACK FULLY VERIFIED LOCALLY." -ForegroundColor Green
    Write-Host "==========================================================" -ForegroundColor Cyan
    exit 0
} else {
    Write-Host "SOME ASSERTIONS FAILED. CHECK LOGS ABOVE." -ForegroundColor Red
    Write-Host "==========================================================" -ForegroundColor Cyan
    exit 1
}
