#!/bin/bash

echo "Building both binary and Docker image..."

# Build binary
./build-binary.sh
if [ $? -ne 0 ]; then
    echo "Binary build failed, stopping..."
    exit 1
fi

# Build Docker
./build-docker.sh
if [ $? -ne 0 ]; then
    echo "Docker build failed"
    exit 1
fi

echo "✅ All builds complete!"