#!/bin/bash

echo "Starting decentralized sequencer validator..."

# Check if .env exists
if [ ! -f .env ]; then
    echo "ERROR: .env file not found!"
    echo "Please copy .env.validator1.example to .env and configure it"
    exit 1
fi

# Source the .env to display which validator is starting
export $(cat .env | grep SEQUENCER_ID | xargs)
echo "Starting validator: ${SEQUENCER_ID:-default}"

# Start with docker-compose
if command -v docker-compose &> /dev/null; then
    docker-compose up -d
else
    docker compose up -d
fi

echo "Validator started. Check logs with: docker-compose logs -f"