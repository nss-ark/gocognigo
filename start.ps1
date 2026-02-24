# GoCognigo - Startup Script
# Usage: .\start.ps1

$ErrorActionPreference = "Stop"
$ProjectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ProjectDir

Write-Host ""
Write-Host "  GoCognigo - Document Intelligence Engine" -ForegroundColor Cyan
Write-Host "  ==========================================" -ForegroundColor DarkCyan
Write-Host ""

# --- 1. Check Go installation ---
Write-Host "[1/5] Checking Go installation..." -ForegroundColor Yellow
try {
    $goVersion = & go version 2>&1
    Write-Host "  $goVersion" -ForegroundColor Green
} catch {
    Write-Host "  ERROR: Go is not installed or not in PATH." -ForegroundColor Red
    Write-Host "  Install from: https://go.dev/dl/" -ForegroundColor Gray
    exit 1
}

# --- 2. Check .env file ---
Write-Host "[2/5] Checking .env configuration..." -ForegroundColor Yellow
if (Test-Path "$ProjectDir\.env") {
    Write-Host "  .env found" -ForegroundColor Green
} elseif (Test-Path "$ProjectDir\.env.example") {
    Copy-Item "$ProjectDir\.env.example" "$ProjectDir\.env"
    Write-Host "  Created .env from .env.example - please edit with your API keys!" -ForegroundColor Yellow
} else {
    Write-Host "  WARNING: No .env file found. Server will use defaults." -ForegroundColor Yellow
}

# --- 3. Kill existing process on port 8080 ---
Write-Host "[3/5] Clearing port 8080..." -ForegroundColor Yellow
$existing = Get-NetTCPConnection -LocalPort 8080 -ErrorAction SilentlyContinue
if ($existing) {
    $existing | ForEach-Object {
        try {
            Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue
        } catch { }
    }
    Start-Sleep -Seconds 2
    Write-Host "  Port 8080 cleared" -ForegroundColor Green
} else {
    Write-Host "  Port 8080 is free" -ForegroundColor Green
}

# --- 4. Build ---
Write-Host "[4/5] Building server..." -ForegroundColor Yellow
& go build -o "$ProjectDir\gocognigo.exe" ./cmd/server/
if ($LASTEXITCODE -ne 0) {
    Write-Host "  BUILD FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "  Build successful" -ForegroundColor Green

# --- 5. Run ---
Write-Host "[5/5] Starting server..." -ForegroundColor Yellow
Write-Host ""
Write-Host "  Server: http://localhost:8080" -ForegroundColor Cyan
Write-Host "  Press Ctrl+C to stop" -ForegroundColor Gray
Write-Host ""

& "$ProjectDir\gocognigo.exe"
