#!/bin/bash

echo "Building decentralized sequencer..."

# Build Go binary first (for local testing if needed)
if [ -f "cmd/sequencer/main.go" ]; then
    echo "Building Go binary..."
    cd cmd/sequencer && go build -o ../../sequencer . && cd ../..
elif [ -f "main.go" ]; then
    echo "Building Go binary from main.go..."
    go build -o sequencer .
fi

# Build Docker image
echo "Building Docker image..."
if command -v docker-compose &> /dev/null; then
    docker-compose build --no-cache
else
    docker compose build --no-cache
fi

echo "Build complete!"