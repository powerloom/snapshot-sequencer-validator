#!/bin/bash

echo "Building Sequencer Consensus Test Docker image..."

# Build Docker image for consensus test
if command -v docker-compose &> /dev/null; then
    docker-compose -f docker-compose.yml build --no-cache
else
    docker compose -f docker-compose.yml build --no-cache
fi

if [ $? -eq 0 ]; then
    echo "✅ Sequencer Consensus Test Docker image built successfully"
    echo "   Use ./start.sh to run the consensus test"
else
    echo "❌ Docker build failed"
    exit 1
fi