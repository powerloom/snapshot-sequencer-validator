#!/bin/bash

echo "⚠️  DEPRECATED: Use specific build scripts instead:"
echo ""
echo "  ./build-consensus-test.sh      - Build for consensus testing (start.sh)"
echo "  ./build-snapshot-sequencer.sh  - Build for snapshot sequencer (launch.sh)"
echo ""
echo "Building default (consensus test) for backwards compatibility..."

# Build Docker image
if command -v docker-compose &> /dev/null; then
    docker-compose build
else
    docker compose build
fi

if [ $? -eq 0 ]; then
    echo "✅ Docker image built successfully"
else
    echo "❌ Docker build failed"
    exit 1
fi