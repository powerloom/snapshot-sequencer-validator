#!/bin/bash

echo "Building decentralized sequencer..."

# Build Go binary first (for local testing if needed)
if [ -f "cmd/validator/main.go" ]; then
    echo "Building Go binary..."
    go build -o validator ./cmd/validator/main.go
elif [ -f "cmd/main.go" ]; then
    echo "Building Go binary from cmd/main.go..."
    go build -o sequencer ./cmd/main.go
fi

# Build Docker image
echo "Building Docker image..."
if command -v docker-compose &> /dev/null; then
    docker-compose build --no-cache
else
    docker compose build --no-cache
fi

echo "Build complete!"