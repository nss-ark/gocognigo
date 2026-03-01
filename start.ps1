# GoCognigo - One-Click Startup Script
# Usage: .\start.ps1
# Automatically installs all dependencies (Go, Tesseract, Poppler) if missing.
# Designed to work on a completely fresh Windows system.

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
# Helper: Refresh PATH from registry (picks up new installs)
# ===================================================================
function Refresh-Path {
    $machinePath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    $env:PATH = "$machinePath;$userPath"
}

# ===================================================================
# 1. Check / Install Go
# ===================================================================
Write-Host "[1/7] Checking Go installation..." -ForegroundColor Yellow

$goFound = $false
try {
    $goVersion = & go version 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host "  $goVersion" -ForegroundColor Green
        $goFound = $true
    }
} catch { }

if (-not $goFound) {
    Write-Host "  Go not found. Attempting auto-install..." -ForegroundColor Yellow

    # Try winget first
    $wingetInstalled = $false
    try {
        $wingetCheck = Get-Command winget -ErrorAction SilentlyContinue
        if ($wingetCheck) {
            Write-Host "  Installing Go via winget..." -ForegroundColor Gray
            & winget install -e --id GoLang.Go --accept-package-agreements --accept-source-agreements --silent 2>&1 | Out-Null
            Refresh-Path
            try {
                $goVersion = & go version 2>&1
                if ($LASTEXITCODE -eq 0) {
                    Write-Host "  $goVersion (installed via winget)" -ForegroundColor Green
                    $wingetInstalled = $true
                    $goFound = $true
                }
            } catch { }
        }
    } catch { }

    # Fallback: download MSI installer
    if (-not $wingetInstalled) {
        Write-Host "  Trying direct MSI install..." -ForegroundColor Gray
        $goMsi = Join-Path $ToolsDir "go-installer.msi"
        if (-not (Test-Path $ToolsDir)) { New-Item -ItemType Directory -Path $ToolsDir -Force | Out-Null }

        # Detect architecture
        $arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
        $goUrl = "https://go.dev/dl/go1.25.5.windows-$arch.msi"

        $downloaded = Download-File -Url $goUrl -OutFile $goMsi -Description "Go installer"
        if ($downloaded) {
            Write-Host "  Running Go installer (this may take a minute)..." -ForegroundColor Gray
            Start-Process msiexec.exe -ArgumentList "/i `"$goMsi`" /quiet /norestart" -Wait -NoNewWindow
            Remove-Item $goMsi -Force -ErrorAction SilentlyContinue
            Refresh-Path

            # Also add common Go install path manually in case MSI didn't update current session
            $defaultGoPath = "C:\Program Files\Go\bin"
            if ((Test-Path $defaultGoPath) -and ($env:PATH -notlike "*$defaultGoPath*")) {
                $env:PATH = "$defaultGoPath;$env:PATH"
            }

            try {
                $goVersion = & go version 2>&1
                if ($LASTEXITCODE -eq 0) {
                    Write-Host "  $goVersion (installed via MSI)" -ForegroundColor Green
                    $goFound = $true
                }
            } catch { }
        }
    }

    if (-not $goFound) {
        Write-Host "  ERROR: Could not install Go automatically." -ForegroundColor Red
        Write-Host "  Please install Go manually from: https://go.dev/dl/" -ForegroundColor Gray
        Write-Host "  After installing, restart this script." -ForegroundColor Gray
        exit 1
    }
}

# ===================================================================
# 2. Check .env file
# ===================================================================
Write-Host "[2/7] Checking .env configuration..." -ForegroundColor Yellow
if (Test-Path "$ProjectDir\.env") {
    Write-Host "  .env found" -ForegroundColor Green
} elseif (Test-Path "$ProjectDir\.env.example") {
    Copy-Item "$ProjectDir\.env.example" "$ProjectDir\.env"
    Write-Host "  Created .env from .env.example - edit with your API keys!" -ForegroundColor Yellow
} else {
    Write-Host "  WARNING: No .env file found. Server will use defaults." -ForegroundColor Yellow
}

# ===================================================================
# 3. Setup OCR Tools (Tesseract + Poppler)
# ===================================================================
Write-Host "[3/7] Checking OCR dependencies..." -ForegroundColor Yellow

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
    # Check if pdftoppm exists anywhere under popplerDir (alternate structure)
    $altPdftoppm = $null
    if (Test-Path $popplerDir) {
        $altPdftoppm = Get-ChildItem -Path $popplerDir -Recurse -Filter "pdftoppm.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
    }

    if ($altPdftoppm) {
        $popplerBin = $altPdftoppm.DirectoryName
        Write-Host "  Poppler: found at $popplerBin" -ForegroundColor Green
    } else {
        Write-Host "  Poppler: not found, installing..." -ForegroundColor Yellow
        $popplerUrl = "https://github.com/oschwartz10612/poppler-windows/releases/download/v24.08.0-0/Release-24.08.0-0.zip"
        $popplerZip = Join-Path $ToolsDir "poppler.zip"

        $downloaded = Download-File -Url $popplerUrl -OutFile $popplerZip -Description "Poppler (PDF tools)"

        if ($downloaded) {
            Write-Host "  Extracting Poppler..." -ForegroundColor Gray
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
}

# --- Tesseract OCR ---
$tesseractDir = Join-Path $ToolsDir "Tesseract-OCR"
$tesseractExe = Join-Path $tesseractDir "tesseract.exe"
# Also check old path for backward compatibility
$tesseractDirOld = Join-Path $ToolsDir "tesseract"
$tesseractExeOld = Join-Path $tesseractDirOld "tesseract.exe"

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

$tesseractBinDir = $null

if ($sysTesseract) {
    Write-Host "  Tesseract: found at $sysTesseract" -ForegroundColor Green
    $tesseractBinDir = Split-Path -Parent $sysTesseract
} elseif (Test-Path $tesseractExe) {
    Write-Host "  Tesseract: found (cached in tools/Tesseract-OCR/)" -ForegroundColor Green
    $tesseractBinDir = $tesseractDir
} elseif (Test-Path $tesseractExeOld) {
    Write-Host "  Tesseract: found (cached in tools/tesseract/)" -ForegroundColor Green
    $tesseractBinDir = $tesseractDirOld
} else {
    Write-Host "  Tesseract: not found, attempting install..." -ForegroundColor Yellow

    # Strategy 1: Try winget (most reliable on modern Windows)
    $wingetOk = $false
    try {
        $wingetCheck = Get-Command winget -ErrorAction SilentlyContinue
        if ($wingetCheck) {
            Write-Host "  Trying winget install..." -ForegroundColor Gray
            $result = & winget install -e --id UB-Mannheim.TesseractOCR --accept-package-agreements --accept-source-agreements --silent 2>&1
            Refresh-Path
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

    # Strategy 2: Download installer and run silently to tools/ directory
    if (-not $wingetOk) {
        Write-Host "  winget unavailable or failed, downloading installer..." -ForegroundColor Gray
        $tessUrl = "https://github.com/UB-Mannheim/tesseract/releases/download/v5.5.0.20241111/tesseract-ocr-w64-setup-5.5.0.20241111.exe"
        $tessInstaller = Join-Path $ToolsDir "tesseract-installer.exe"

        $downloaded = Download-File -Url $tessUrl -OutFile $tessInstaller -Description "Tesseract OCR"
        if ($downloaded) {
            Write-Host "  Running Tesseract installer to tools/Tesseract-OCR/..." -ForegroundColor Gray
            # Install silently to the local tools directory (no admin required)
            try {
                Start-Process $tessInstaller -ArgumentList "/S /D=$tesseractDir" -Wait -NoNewWindow
                Start-Sleep -Seconds 3
            } catch {
                Write-Host "  Installer execution failed: $_" -ForegroundColor Yellow
            }
            Remove-Item $tessInstaller -Force -ErrorAction SilentlyContinue

            if (Test-Path $tesseractExe) {
                Write-Host "  Tesseract: installed to tools/Tesseract-OCR/" -ForegroundColor Green
                $tesseractBinDir = $tesseractDir
            } else {
                # Check if it ended up system-installed instead
                Refresh-Path
                foreach ($tp in $tesseractPaths) {
                    if ($tp -and (Test-Path $tp)) {
                        $sysTesseract = $tp
                        $tesseractBinDir = Split-Path -Parent $tp
                        Write-Host "  Tesseract: installed at $sysTesseract" -ForegroundColor Green
                        break
                    }
                }
            }
        }
    }

    if (-not $tesseractBinDir) {
        Write-Host "  Tesseract: auto-install failed." -ForegroundColor Yellow
        Write-Host "  Install manually from: https://github.com/UB-Mannheim/tesseract/wiki" -ForegroundColor Gray
        Write-Host "  OCR will fall back to Sarvam API if configured." -ForegroundColor Gray
    }
}

# --- Add tools to PATH for this session ---
$pathAdditions = @()
if (Test-Path $popplerBin) { $pathAdditions += $popplerBin }
if ($tesseractBinDir -and (Test-Path $tesseractBinDir)) { $pathAdditions += $tesseractBinDir }
if ($pathAdditions.Count -gt 0) {
    $env:PATH = ($pathAdditions -join ";") + ";" + $env:PATH
    Write-Host "  Added $($pathAdditions.Count) tool(s) to PATH" -ForegroundColor Gray
}

# ===================================================================
# 4. Ensure data directory exists
# ===================================================================
Write-Host "[4/7] Ensuring data directories..." -ForegroundColor Yellow
$dataDir = Join-Path $ProjectDir "data"
if (-not (Test-Path $dataDir)) {
    New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    Write-Host "  Created data/ directory" -ForegroundColor Green
} else {
    Write-Host "  data/ directory exists" -ForegroundColor Green
}

# ===================================================================
# 5. Kill existing process on port 8080
# ===================================================================
Write-Host "[5/7] Clearing port 8080..." -ForegroundColor Yellow
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
# 6. Resolve dependencies & Build
# ===================================================================
Write-Host "[6/7] Resolving dependencies & building..." -ForegroundColor Yellow

# Always run go mod tidy to ensure dependencies are resolved
# (critical for freshly cloned repos or after go.mod changes)
Write-Host "  Running go mod tidy..." -ForegroundColor Gray
& go mod tidy 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "  WARNING: go mod tidy returned errors (build may still succeed)" -ForegroundColor Yellow
}

Write-Host "  Building server..." -ForegroundColor Gray
& go build -o "$ProjectDir\gocognigo.exe" ./cmd/server/
if ($LASTEXITCODE -ne 0) {
    Write-Host "  BUILD FAILED" -ForegroundColor Red
    Write-Host "  Try running 'go mod tidy' manually and check for errors." -ForegroundColor Gray
    exit 1
}
Write-Host "  Build successful" -ForegroundColor Green

# ===================================================================
# 7. Run server & open browser
# ===================================================================
# Determine port from .env or default
$port = "8080"
if (Test-Path "$ProjectDir\.env") {
    $envContent = Get-Content "$ProjectDir\.env" -ErrorAction SilentlyContinue
    foreach ($line in $envContent) {
        if ($line -match "^\s*PORT\s*=\s*(\d+)") {
            $port = $Matches[1]
        }
    }
}

$serverUrl = "http://localhost:$port"

Write-Host "[7/7] Starting server..." -ForegroundColor Yellow
Write-Host ""
Write-Host "  Server: $serverUrl" -ForegroundColor Cyan
Write-Host "  Press Ctrl+C to stop" -ForegroundColor Gray
Write-Host ""

# Auto-open browser after a short delay (give server time to start)
$browserJob = Start-Job -ScriptBlock {
    param($url)
    Start-Sleep -Seconds 2
    Start-Process $url
} -ArgumentList $serverUrl

# Run the server (blocking)
try {
    & "$ProjectDir\gocognigo.exe"
} finally {
    # Clean up background job if server exits
    Stop-Job $browserJob -ErrorAction SilentlyContinue
    Remove-Job $browserJob -ErrorAction SilentlyContinue
}
