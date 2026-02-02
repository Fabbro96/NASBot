#!/bin/bash

# Build script for NASBot release

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  🛠  NASBot Build Script${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"

# Clean previous builds
rm -f nasbot nasbot-arm64

echo -e "${YELLOW}Building for current architecture...${NC}"
go build -o nasbot .
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Success: nasbot${NC}"
else
    echo -e "${RED}❌ Failed to build nasbot${NC}"
    exit 1
fi

echo -e "${YELLOW}Building for generic ARM64 (Linux)...${NC}"
export CGO_ENABLED=0
GOOS=linux GOARCH=arm64 go build -o nasbot-arm64 .
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Success: nasbot-arm64${NC}"
else
    echo -e "${RED}❌ Failed to build nasbot-arm64${NC}"
    exit 1
fi

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}🎉 Build complete!${NC}"
ls -lh nasbot nasbot-arm64
