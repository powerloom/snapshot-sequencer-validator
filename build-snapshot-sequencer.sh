#!/bin/bash

echo "Building Snapshot Sequencer Docker image..."

# Build Docker image for snapshot sequencer
if command -v docker-compose &> /dev/null; then
    docker-compose -f docker-compose.snapshot-sequencer.yml build --no-cache
else
    docker compose -f docker-compose.snapshot-sequencer.yml build --no-cache
fi

if [ $? -eq 0 ]; then
    echo "✅ Snapshot Sequencer Docker image built successfully"
    echo "   Use ./launch.sh sequencer-custom to run with your .env settings"
    echo "   Use ./launch.sh sequencer-all to run with all components enabled"
else
    echo "❌ Docker build failed"
    exit 1
fi