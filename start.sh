#!/bin/bash
# GoCognigo - One-Click Startup Script for macOS & Linux
# Automatically installs all dependencies (Go, Tesseract, Poppler) if missing.

set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "\n${GREEN}  GoCognigo - Document Intelligence Engine${NC}"
echo -e "${GREEN}  ==========================================${NC}\n"

# 1. Detect OS & Package Manager
OS="$(uname -s)"
if [ "$OS" = "Darwin" ]; then
    echo -e "${YELLOW}[1/4] Detected macOS...${NC}"
    if ! command -v brew &> /dev/null; then
        echo -e "${RED}Homebrew is required on macOS. Please install it from https://brew.sh${NC}"
        exit 1
    fi
    PKG_MGR="brew install"
elif [ "$OS" = "Linux" ]; then
    echo -e "${YELLOW}[1/4] Detected Linux...${NC}"
    if command -v apt-get &> /dev/null; then
        # Check if we have sudo privileges
        if [ "$EUID" -ne 0 ] && command -v sudo &> /dev/null; then
            PKG_MGR="sudo apt-get install -y"
            # Update apt just in case
            sudo apt-get update -y || true
        else
            PKG_MGR="apt-get install -y"
            apt-get update -y || true
        fi
    else
        echo -e "${RED}Only apt-based Linux distributions (Debian/Ubuntu) are fully supported by this auto-script right now.${NC}"
        echo -e "Please install 'golang', 'poppler-utils', and 'tesseract-ocr' manually."
        exit 1
    fi
else
    echo -e "${RED}Unsupported OS: $OS. Please use start.ps1 for Windows.${NC}"
    exit 1
fi

# 2. Check / Install Go
echo -e "${YELLOW}[2/4] Checking Go installation...${NC}"
if ! command -v go &> /dev/null; then
    echo -e "  Go not found. Attempting to install..."
    if [ "$OS" = "Darwin" ]; then
        $PKG_MGR go
    else
        $PKG_MGR golang-go
    fi
else
    echo -e "  ${GREEN}$(go version)${NC}"
fi

# 3. Check / Install OCR Tools (Poppler & Tesseract)
echo -e "${YELLOW}[3/4] Checking OCR dependencies...${NC}"

if ! command -v pdftoppm &> /dev/null; then
    echo -e "  Poppler not found. Installing..."
    if [ "$OS" = "Darwin" ]; then
        $PKG_MGR poppler
    else
        $PKG_MGR poppler-utils
    fi
else
    echo -e "  ${GREEN}Poppler: found${NC}"
fi

if ! command -v tesseract &> /dev/null; then
    echo -e "  Tesseract not found. Installing OCR engine..."
    if [ "$OS" = "Darwin" ]; then
        $PKG_MGR tesseract
        # brew installs english by default
    else
        $PKG_MGR tesseract-ocr tesseract-ocr-eng
    fi
else
    echo -e "  ${GREEN}Tesseract: found${NC}"
fi

# 4. Check .env file
echo -e "${YELLOW}[4/4] Checking .env configuration...${NC}"
if [ -f ".env" ]; then
    echo -e "  ${GREEN}.env found${NC}"
elif [ -f ".env.example" ]; then
    cp .env.example .env
    echo -e "  ${YELLOW}Created .env from .env.example - edit carefully with your API keys!${NC}"
else
    echo -e "  ${YELLOW}WARNING: No .env file found. Server will use defaults.${NC}"
fi

# 5. Start the Server
echo -e "\n${GREEN}All dependencies met. Downloading Go modules and starting server...${NC}"
go mod tidy
go run ./cmd/server
