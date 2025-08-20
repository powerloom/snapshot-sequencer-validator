#!/bin/bash

echo "Building Docker image..."

# Build Docker image
if command -v docker-compose &> /dev/null; then
    docker-compose build --no-cache
else
    docker compose build --no-cache
fi

if [ $? -eq 0 ]; then
    echo "✅ Docker image built successfully"
else
    echo "❌ Docker build failed"
    exit 1
fi