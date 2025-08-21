#!/bin/bash

echo "Building Go binaries..."

# Create bin directory if it doesn't exist
mkdir -p bin

# Build the sequencer-consensus-test binary
if [ -f "cmd/sequencer-consensus-test/main.go" ]; then
    echo "Building sequencer-consensus-test binary..."
    go build -o bin/sequencer-consensus-test ./cmd/sequencer-consensus-test/main.go
    if [ $? -eq 0 ]; then
        echo "✅ Sequencer consensus test built successfully: bin/sequencer-consensus-test"
    else
        echo "❌ Sequencer consensus test build failed"
        exit 1
    fi
else
    echo "⚠️ cmd/sequencer-consensus-test/main.go not found, skipping"
fi

# Build the unified binary
if [ -f "cmd/unified/main.go" ]; then
    echo "Building unified binary..."
    go build -o bin/unified ./cmd/unified/main.go
    if [ $? -eq 0 ]; then
        echo "✅ Unified built successfully: bin/unified"
    else
        echo "❌ Unified build failed"
        exit 1
    fi
else
    echo "⚠️ cmd/unified/main.go not found, skipping"
fi

echo "✅ All binaries built in bin/ directory"