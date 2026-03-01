# GoCognigo - One-Click Startup Script
# Usage: .\start.ps1
# Automatically installs all dependencies (Go, Tesseract, Poppler) if missing.

$ErrorActionPreference = "Stop"
$ProjectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ProjectDir
$ToolsDir = Join-Path $ProjectDir "tools"

Write-Host ""
Write-Host "  GoCognigo - Document Intelligence Engine" -ForegroundColor Cyan
Write-Host "  ==========================================" -ForegroundColor DarkCyan
Write-Host ""

# ===================================================================
# Helper: Download a file with retry logic
# ===================================================================
function Download-File {
    param([string]$Url, [string]$OutFile, [string]$Description)
    Write-Host "  Downloading $Description..." -ForegroundColor Gray
    $retries = 3
    for ($i = 1; $i -le $retries; $i++) {
        try {
            $ProgressPreference = 'SilentlyContinue'
            Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing `
                -UserAgent "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
            return $true
        } catch {
            if ($i -eq $retries) {
                Write-Host "  Failed to download after $retries attempts: $_" -ForegroundColor Red
                return $false
            }
            Write-Host "  Retry $i/$retries..." -ForegroundColor Yellow
            Start-Sleep -Seconds 2
        }
    }
    return $false
}

# ===================================================================
# 1. Check Go installation
# ===================================================================
Write-Host "[1/6] Checking Go installation..." -ForegroundColor Yellow
try {
    $goVersion = & go version 2>&1
    Write-Host "  $goVersion" -ForegroundColor Green
} catch {
    Write-Host "  ERROR: Go is not installed or not in PATH." -ForegroundColor Red
    Write-Host "  Install from: https://go.dev/dl/" -ForegroundColor Gray
    exit 1
}

# ===================================================================
# 2. Check .env file
# ===================================================================
Write-Host "[2/6] Checking .env configuration..." -ForegroundColor Yellow
if (Test-Path "$ProjectDir\.env") {
    Write-Host "  .env found" -ForegroundColor Green
} elseif (Test-Path "$ProjectDir\.env.example") {
    Copy-Item "$ProjectDir\.env.example" "$ProjectDir\.env"
    Write-Host "  Created .env from .env.example - please edit with your API keys!" -ForegroundColor Yellow
} else {
    Write-Host "  WARNING: No .env file found. Server will use defaults." -ForegroundColor Yellow
}

# ===================================================================
# 3. Setup OCR Tools (Tesseract + Poppler)
# ===================================================================
Write-Host "[3/6] Checking OCR dependencies..." -ForegroundColor Yellow

# Ensure tools directory exists
if (-not (Test-Path $ToolsDir)) {
    New-Item -ItemType Directory -Path $ToolsDir -Force | Out-Null
}

# --- Poppler (provides pdftoppm for PDF-to-image conversion) ---
$popplerDir = Join-Path $ToolsDir "poppler"
$popplerBin = Join-Path $popplerDir "Library\bin"
$pdftoppmPath = Join-Path $popplerBin "pdftoppm.exe"

if (Test-Path $pdftoppmPath) {
    Write-Host "  Poppler: found (cached)" -ForegroundColor Green
} else {
    Write-Host "  Poppler: not found, installing..." -ForegroundColor Yellow
    $popplerUrl = "https://github.com/oschwartz10612/poppler-windows/releases/download/v24.08.0-0/Release-24.08.0-0.zip"
    $popplerZip = Join-Path $ToolsDir "poppler.zip"

    $downloaded = Download-File -Url $popplerUrl -OutFile $popplerZip -Description "Poppler (PDF tools)"

    if ($downloaded) {
        Write-Host "  Extracting Poppler..." -ForegroundColor Gray
        # Extract to temp, then move the inner folder
        $extractTemp = Join-Path $ToolsDir "poppler-extract"
        if (Test-Path $extractTemp) { Remove-Item $extractTemp -Recurse -Force }
        Expand-Archive -Path $popplerZip -DestinationPath $extractTemp -Force

        # Find the extracted directory (usually "poppler-xx.xx.x")
        $innerDir = Get-ChildItem -Path $extractTemp -Directory | Select-Object -First 1
        if ($innerDir) {
            if (Test-Path $popplerDir) { Remove-Item $popplerDir -Recurse -Force }
            Move-Item -Path $innerDir.FullName -Destination $popplerDir -Force
        }

        # Cleanup
        Remove-Item $popplerZip -Force -ErrorAction SilentlyContinue
        Remove-Item $extractTemp -Recurse -Force -ErrorAction SilentlyContinue

        if (Test-Path $pdftoppmPath) {
            Write-Host "  Poppler: installed successfully" -ForegroundColor Green
        } else {
            # Try alternate directory structure
            $altBin = Get-ChildItem -Path $popplerDir -Recurse -Filter "pdftoppm.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($altBin) {
                $popplerBin = $altBin.DirectoryName
                Write-Host "  Poppler: installed (at $popplerBin)" -ForegroundColor Green
            } else {
                Write-Host "  Poppler: install failed (OCR for scanned PDFs may not work)" -ForegroundColor Yellow
            }
        }
    } else {
        Write-Host "  Poppler: download failed (OCR will be limited)" -ForegroundColor Yellow
    }
}

# --- Tesseract OCR ---
$tesseractDir = Join-Path $ToolsDir "tesseract"
$tesseractExe = Join-Path $tesseractDir "tesseract.exe"

# Check system-installed Tesseract first
$sysTesseract = $null
$tesseractPaths = @(
    (Get-Command "tesseract" -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
    "C:\Program Files\Tesseract-OCR\tesseract.exe",
    "C:\Program Files\Tesseract\tesseract.exe",
    "C:\Program Files (x86)\Tesseract-OCR\tesseract.exe"
)
foreach ($tp in $tesseractPaths) {
    if ($tp -and (Test-Path $tp)) {
        $sysTesseract = $tp
        break
    }
}

if ($sysTesseract) {
    Write-Host "  Tesseract: found at $sysTesseract" -ForegroundColor Green
    $tesseractBinDir = Split-Path -Parent $sysTesseract
} elseif (Test-Path $tesseractExe) {
    Write-Host "  Tesseract: found (cached in tools/)" -ForegroundColor Green
    $tesseractBinDir = $tesseractDir
} else {
    Write-Host "  Tesseract: not found, attempting install..." -ForegroundColor Yellow

    # Try winget first (most reliable on modern Windows)
    $wingetOk = $false
    try {
        $wingetCheck = Get-Command winget -ErrorAction SilentlyContinue
        if ($wingetCheck) {
            Write-Host "  Trying winget install..." -ForegroundColor Gray
            $result = & winget install -e --id UB-Mannheim.TesseractOCR --accept-package-agreements --accept-source-agreements --silent 2>&1
            # Check if install succeeded
            foreach ($tp in $tesseractPaths) {
                if ($tp -and (Test-Path $tp)) {
                    $sysTesseract = $tp
                    $tesseractBinDir = Split-Path -Parent $tp
                    $wingetOk = $true
                    break
                }
            }
        }
    } catch { }

    if ($wingetOk) {
        Write-Host "  Tesseract: installed via winget" -ForegroundColor Green
    } else {
        Write-Host "  Tesseract: auto-install failed." -ForegroundColor Yellow
        Write-Host "  Please install manually from: https://github.com/UB-Mannheim/tesseract/wiki" -ForegroundColor Gray
        Write-Host "  OCR will fall back to Sarvam API (requires SARVAM_API_KEY in .env)" -ForegroundColor Gray
        $tesseractBinDir = $null
    }
}

# --- Add tools to PATH for this session ---
$pathAdditions = @()
if (Test-Path $popplerBin) { $pathAdditions += $popplerBin }
if ($tesseractBinDir) { $pathAdditions += $tesseractBinDir }
if ($pathAdditions.Count -gt 0) {
    $env:PATH = ($pathAdditions -join ";") + ";" + $env:PATH
    Write-Host "  Added $($pathAdditions.Count) tool(s) to PATH" -ForegroundColor Gray
}

# ===================================================================
# 4. Kill existing process on port 8080
# ===================================================================
Write-Host "[4/6] Clearing port 8080..." -ForegroundColor Yellow
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

# ===================================================================
# 5. Build
# ===================================================================
Write-Host "[5/6] Building server..." -ForegroundColor Yellow
& go build -o "$ProjectDir\gocognigo.exe" ./cmd/server/
if ($LASTEXITCODE -ne 0) {
    Write-Host "  BUILD FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "  Build successful" -ForegroundColor Green

# ===================================================================
# 6. Run
# ===================================================================
Write-Host "[6/6] Starting server..." -ForegroundColor Yellow
Write-Host ""
Write-Host "  Server: http://localhost:8080" -ForegroundColor Cyan
Write-Host "  Press Ctrl+C to stop" -ForegroundColor Gray
Write-Host ""

& "$ProjectDir\gocognigo.exe"
