#!/bin/bash

# Build script for NASBot release

echo "════════════════════════════════════════════"
echo "  NASBot Build Script"
echo "════════════════════════════════════════════"

# Clean previous builds
rm -f nasbot nasbot-arm64

echo "🛠  Building for current architecture..."
go build -o nasbot .
if [ $? -eq 0 ]; then
    echo "✅ Success: nasbot"
else
    echo "❌ Failed to build nasbot"
    exit 1
fi

echo "🛠  Building for generic ARM64 (Linux)..."
export CGO_ENABLED=0
GOOS=linux GOARCH=arm64 go build -o nasbot-arm64 .
if [ $? -eq 0 ]; then
    echo "✅ Success: nasbot-arm64"
else
    echo "❌ Failed to build nasbot-arm64"
    exit 1
fi

echo "════════════════════════════════════════════"
echo "🎉 Build complete!"
ls -lh nasbot nasbot-arm64
