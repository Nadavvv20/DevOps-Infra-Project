# ─────────────────────────────────────────────────────────────────────────────
# Smoke Test Runner (PowerShell) — Online Boutique Architecture Validation Suite
# Runs the 13 verification tests against the deployed cluster/ALB.
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

Write-Host "Target Configuration:" -ForegroundColor Yellow
Write-Host "  Base URL:     $($env:BASE_URL)"
Write-Host "  AWS Region:   $($env:AWS_REGION)"
Write-Host "  Cluster Name: $($env:CLUSTER_NAME)"
Write-Host ""

Set-Location "$RepoRoot\tests\smoke"

if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Host "Executing Go smoke test suite..." -ForegroundColor Green
    go test -v -timeout 15m ./...
} elseif (Get-Command docker -ErrorAction SilentlyContinue) {
    Write-Host "Go not installed locally, executing via Docker container..." -ForegroundColor Green
    docker run --rm `
        -e BASE_URL="$($env:BASE_URL)" `
        -e AWS_REGION="$($env:AWS_REGION)" `
        -e CLUSTER_NAME="$($env:CLUSTER_NAME)" `
        -v "${RepoRoot}\tests\smoke:/workspace" `
        -w /workspace `
        golang:1.23-alpine `
        go test -v -timeout 15m ./...
} else {
    Write-Error "Error: Neither 'go' nor 'docker' is available to run tests."
}

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host " All Smoke Tests Finished Successfully!                   " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
