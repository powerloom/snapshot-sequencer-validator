#!/bin/bash

echo "Building Go binary..."

# Build the validator binary
if [ -f "cmd/validator/main.go" ]; then
    echo "Building validator binary..."
    go build -o validator ./cmd/validator/main.go
    if [ $? -eq 0 ]; then
        echo "✅ Binary built successfully: ./validator"
    else
        echo "❌ Binary build failed"
        exit 1
    fi
else
    echo "❌ cmd/validator/main.go not found"
    exit 1
fi