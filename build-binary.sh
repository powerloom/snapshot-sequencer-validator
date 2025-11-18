#!/bin/bash

echo "Building Go binaries..."

# Create bin directory if it doesn't exist
mkdir -p bin

# sequencer-consensus-test removed - obsolete architecture

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

# Build the P2P Gateway binary
if [ -f "cmd/p2p-gateway/main.go" ]; then
    echo "Building p2p-gateway binary..."
    go build -o bin/p2p-gateway ./cmd/p2p-gateway/main.go
    if [ $? -eq 0 ]; then
        echo "✅ P2P Gateway built successfully: bin/p2p-gateway"
    else
        echo "❌ P2P Gateway build failed"
        exit 1
    fi
else
    echo "⚠️ cmd/p2p-gateway/main.go not found, skipping"
fi

# Build the Aggregator binary
if [ -f "cmd/aggregator/main.go" ]; then
    echo "Building aggregator binary..."
    go build -o bin/aggregator ./cmd/aggregator/main.go
    if [ $? -eq 0 ]; then
        echo "✅ Aggregator built successfully: bin/aggregator"
    else
        echo "❌ Aggregator build failed"
        exit 1
    fi
else
    echo "⚠️ cmd/aggregator/main.go not found, skipping"
fi

echo "✅ All binaries built in bin/ directory"