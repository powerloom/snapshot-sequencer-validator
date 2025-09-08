#!/bin/bash

# Simple monitoring script that uses docker exec with redis commands
# This works even if redis-cli is not installed in the container

# Color definitions
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

echo -e "${CYAN}📊 Sequencer Status Monitor${NC}"
echo -e "${CYAN}═══════════════════════════════════${NC}"
echo ""

# Find Redis container
REDIS_CONTAINER=$(docker ps --filter "name=redis" --format "{{.Names}}" | head -1)

if [ -z "$REDIS_CONTAINER" ]; then
    echo -e "${RED}Error: No Redis container found${NC}"
    echo "Start the sequencer first with: ./launch.sh distributed"
    exit 1
fi

echo -e "${GREEN}Using Redis container: $REDIS_CONTAINER${NC}"
echo ""

# Function to run Redis command
redis_cmd() {
    docker exec $REDIS_CONTAINER redis-cli "$@" 2>/dev/null
}

# Active Windows (Updated format: epoch:market:epochID:window)
echo -e "${BLUE}🔷 Active Submission Windows:${NC}"
WINDOWS=$(redis_cmd KEYS "epoch:*:*:window")
if [ ! -z "$WINDOWS" ]; then
    for window in $WINDOWS; do
        STATUS=$(redis_cmd GET "$window")
        if [ "$STATUS" = "open" ]; then
            # Parse epoch:market:epochID:window format
            MARKET=$(echo "$window" | sed 's/epoch://;s/:.*:window$//' | cut -d: -f1)
            EPOCH=$(echo "$window" | sed 's/^epoch:[^:]*://;s/:window$//')
            TTL=$(redis_cmd TTL "$window")
            echo "  ✓ Market: $MARKET, Epoch: $EPOCH (TTL: ${TTL}s)"
        fi
    done
else
    echo "  None active"
fi

# Submission Queue
echo -e "\n${BLUE}📥 Submission Queue:${NC}"
QUEUE_DEPTH=$(redis_cmd LLEN "submissionQueue")
echo "  Pending: ${QUEUE_DEPTH:-0} submissions"

# Ready Batches (Updated format: protocol:market:batch:ready:epochID)
echo -e "\n${BLUE}📦 Ready Batches:${NC}"
READY=$(redis_cmd KEYS "*:*:batch:ready:*")
if [ ! -z "$READY" ]; then
    for batch in $READY; do
        # Extract protocol:market and epoch from protocol:market:batch:ready:epochID
        PROTOCOL_MARKET=$(echo "$batch" | sed "s/:batch:ready:.*//")
        EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
        echo "  ✓ $PROTOCOL_MARKET - Epoch $EPOCH"
    done
else
    echo "  None"
fi

# Finalized Batches (Updated to look for batch:finalized:epochID keys)
echo -e "\n${BLUE}✅ Recent Finalized Batches:${NC}"
FINALIZED=$(redis_cmd KEYS "batch:finalized:*" | head -5)
if [ ! -z "$FINALIZED" ]; then
    for batch in $FINALIZED; do
        EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
        # Try to get additional metadata if available
        MERKLE=$(redis_cmd HGET "$batch" "merkle_root")
        if [ ! -z "$MERKLE" ]; then
            echo "  ✓ Epoch $EPOCH (Merkle: ${MERKLE:0:12}...)"
        else
            echo "  ✓ Epoch $EPOCH"
        fi
    done
else
    echo "  None"
fi

# Workers
echo -e "\n${BLUE}👷 Active Workers:${NC}"
WORKERS=$(redis_cmd KEYS "worker:*:status")
if [ ! -z "$WORKERS" ]; then
    for worker in $WORKERS; do
        STATUS=$(redis_cmd GET "$worker")
        WORKER_NAME=$(echo "$worker" | sed "s/worker://;s/:status//")
        echo "  $WORKER_NAME: $STATUS"
    done
else
    echo "  No active workers"
fi

# Quick Stats
echo -e "\n${BLUE}📈 Quick Stats:${NC}"
echo "  Queue Depth: ${QUEUE_DEPTH:-0}"
WINDOWS_COUNT=$(echo "$WINDOWS" | grep -c "window" 2>/dev/null || echo "0")
echo "  Active Windows: $WINDOWS_COUNT"

echo ""
echo -e "${CYAN}═══════════════════════════════════${NC}"