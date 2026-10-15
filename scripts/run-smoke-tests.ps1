# ─────────────────────────────────────────────────────────────────────────────
# Smoke Test Runner (PowerShell) — Online Boutique Architecture Validation Suite
# Runs against the deployed cluster/ALB.
# ─────────────────────────────────────────────────────────────────────────────

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host " Starting Online Boutique Architecture Smoke Test Suite   " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

if (-not $env:BASE_URL) {
    if (Get-Command kubectl -ErrorAction SilentlyContinue) {
        $albHost = kubectl get svc ingress-nginx-controller -n ingress-nginx -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>$null
        if ($albHost) {
            $env:BASE_URL = "http://$albHost"
        }
    }
}

if (-not $env:BASE_URL) {
    $env:BASE_URL = "http://localhost:8080"
}
if (-not $env:AWS_REGION) {
    $env:AWS_REGION = "il-central-1"
}
if (-not $env:CLUSTER_NAME) {
    $env:CLUSTER_NAME = "online-boutique-dev"
}
if (-not $env:RUN_DESTRUCTIVE) {
    $env:RUN_DESTRUCTIVE = "false"
}

Write-Host "Target Configuration:" -ForegroundColor Yellow
Write-Host "  Base URL:        $($env:BASE_URL)"
Write-Host "  AWS Region:      $($env:AWS_REGION)"
Write-Host "  Cluster Name:    $($env:CLUSTER_NAME)"
Write-Host "  Run Destructive: $($env:RUN_DESTRUCTIVE)"
Write-Host ""

$testPattern = "."
if ($env:RUN_DESTRUCTIVE -ne "true") {
    Write-Host "Running FAST non-destructive smoke test suite (safe after every deploy)..." -ForegroundColor Yellow
    $testPattern = "Test01|Test04|Test05|Test07|Test09|Test10|Test11|Test12|Test13"
} else {
    Write-Host "Running FULL test suite including DESTRUCTIVE tests (pod kills, DLQ injection)..." -ForegroundColor Red
}

Set-Location "$RepoRoot\tests\smoke"

if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Host "Executing Go smoke test suite..." -ForegroundColor Green
    go test -v -timeout 15m -run "$testPattern" ./...
} else {
    Write-Host "Executing via WSL Go..." -ForegroundColor Green
    $wslPath = $RepoRoot.Replace("\", "/").Replace("C:", "/mnt/c")
    wsl -d Ubuntu -- bash -c "cd '$wslPath/tests/smoke' && ~/go/bin/go test -v -timeout 15m -run '$testPattern' ./..."
}

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host " Smoke Tests Completed!                                   " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
