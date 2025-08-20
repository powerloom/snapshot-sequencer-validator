#!/bin/bash

echo "Stopping decentralized sequencer validator..."

if command -v docker-compose &> /dev/null; then
    docker-compose down
else
    docker compose down
fi

echo "Validator stopped."