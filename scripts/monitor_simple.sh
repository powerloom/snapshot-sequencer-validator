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
    echo "Start the sequencer first with: ./dsv.sh distributed"
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
ACTIVE_COUNT=0
CLOSED_COUNT=0
if [ ! -z "$WINDOWS" ]; then
    for window in $WINDOWS; do
        STATUS=$(redis_cmd GET "$window")
        if [ "$STATUS" = "open" ]; then
            # Parse epoch:market:epochID:window format
            MARKET=$(echo "$window" | sed 's/epoch://;s/:.*:window$//' | cut -d: -f1)
            EPOCH=$(echo "$window" | sed 's/^epoch:[^:]*://;s/:window$//')
            TTL=$(redis_cmd TTL "$window")
            echo "  ✓ Market: $MARKET, Epoch: $EPOCH (TTL: ${TTL}s)"
            ACTIVE_COUNT=$((ACTIVE_COUNT + 1))
        elif [ "$STATUS" = "closed" ]; then
            CLOSED_COUNT=$((CLOSED_COUNT + 1))
        fi
    done
    if [ $ACTIVE_COUNT -eq 0 ]; then
        echo "  None actively open"
    fi
    if [ $CLOSED_COUNT -gt 0 ]; then
        echo "  ℹ️  Note: $CLOSED_COUNT closed windows in Redis (will expire in ~1hr)"
    fi
else
    echo "  None active"
fi

# Submission Queue
echo -e "\n${BLUE}📥 Submission Queue:${NC}"
QUEUE_DEPTH=$(redis_cmd LLEN "submissionQueue")
echo "  Pending: ${QUEUE_DEPTH:-0} submissions"

# Ready Batches (Updated format: protocol:market:batch:ready:epochID)
echo -e "\n${BLUE}📦 Ready Batches (with vote data):${NC}"
READY=$(redis_cmd KEYS "*:*:batch:ready:*")
if [ ! -z "$READY" ]; then
    for batch in $READY; do
        # Extract protocol:market and epoch from protocol:market:batch:ready:epochID
        PROTOCOL_MARKET=$(echo "$batch" | sed "s/:batch:ready:.*//")
        EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
        
        # Get batch data to check format
        DATA=$(redis_cmd GET "$batch")
        if [ ! -z "$DATA" ]; then
            # Count actual project IDs (top-level keys only, excluding cid_votes and total_submissions)
            if command -v jq >/dev/null 2>&1; then
                PROJECT_COUNT=$(echo "$DATA" | jq 'keys | length' 2>/dev/null || echo "0")
            else
                # Fallback if jq not available
                PROJECT_COUNT=$(echo "$DATA" | grep -o '"[^"]*":{' | wc -l)
            fi
            if echo "$DATA" | grep -q '"cid_votes"'; then
                echo -e "  ${GREEN}✓ $PROTOCOL_MARKET - Epoch $EPOCH (${PROJECT_COUNT} projects with FULL vote data)${NC}"
            else
                echo -e "  ${YELLOW}⚠ $PROTOCOL_MARKET - Epoch $EPOCH (${PROJECT_COUNT} projects - OLD pre-selected format)${NC}"
            fi
        else
            echo "  ✓ $PROTOCOL_MARKET - Epoch $EPOCH"
        fi
    done
else
    echo "  None"
fi

# Finalized Batches (Updated to look for batch:finalized:epochID keys)
echo -e "\n${BLUE}✅ Recent Finalized Batches:${NC}"
FINALIZED=$(redis_cmd KEYS "batch:finalized:*" | head -10)
if [ ! -z "$FINALIZED" ]; then
    for batch in $FINALIZED; do
        EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
        # Get all metadata: IPFS CID, merkle root, finalization time
        IPFS_CID=$(redis_cmd HGET "$batch" "ipfs_cid")
        MERKLE=$(redis_cmd HGET "$batch" "merkle_root")
        FINALIZED_AT=$(redis_cmd HGET "$batch" "finalized_at")

        # Format output based on available data
        if [ ! -z "$IPFS_CID" ]; then
            echo -e "  ${GREEN}✓ Epoch $EPOCH${NC}"
            echo "    📦 IPFS CID: $IPFS_CID"
            if [ ! -z "$MERKLE" ]; then
                echo "    🌳 Merkle: ${MERKLE:0:16}..."
            fi
            if [ ! -z "$FINALIZED_AT" ]; then
                # Convert timestamp to readable format if date command available
                if command -v date >/dev/null 2>&1; then
                    FORMATTED_TIME=$(date -d "@$FINALIZED_AT" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || echo "timestamp: $FINALIZED_AT")
                    echo "    ⏰ Finalized: $FORMATTED_TIME"
                fi
            fi
        elif [ ! -z "$MERKLE" ]; then
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

# Check finalization queue
echo -e "\n${BLUE}🔄 Finalization Queue:${NC}"
FIN_QUEUES=$(redis_cmd KEYS "*:*:finalizationQueue")
if [ ! -z "$FIN_QUEUES" ]; then
    for queue in $FIN_QUEUES; do
        QUEUE_LEN=$(redis_cmd LLEN "$queue")
        echo "  $queue: ${QUEUE_LEN:-0} batches pending"
    done
else
    echo "  No finalization queues found"
fi

# Quick Stats
echo -e "\n${BLUE}📈 Quick Stats:${NC}"
echo "  Queue Depth: ${QUEUE_DEPTH:-0}"
echo "  Open Windows: $ACTIVE_COUNT (Total in Redis: $((ACTIVE_COUNT + CLOSED_COUNT)))"

# Vote Distribution Debug (shows if collector is passing all votes)
echo -e "\n${BLUE}🗳️ Vote Distribution Check:${NC}"
SAMPLE_BATCH=$(redis_cmd KEYS "*:*:batch:ready:*" | head -1)
if [ ! -z "$SAMPLE_BATCH" ]; then
    DATA=$(redis_cmd GET "$SAMPLE_BATCH")
    if echo "$DATA" | grep -q '"cid_votes"'; then
        echo -e "  ${GREEN}✓ NEW FORMAT DETECTED: Passing all CIDs with vote counts${NC}"
        # Try to extract a sample project to show vote distribution
        SAMPLE_PROJECT=$(echo "$DATA" | grep -o '"[^"]*":{"cid_votes"' | head -1 | cut -d'"' -f2)
        if [ ! -z "$SAMPLE_PROJECT" ]; then
            echo "  Sample Project: $SAMPLE_PROJECT has multiple CIDs with votes"
        fi
    elif echo "$DATA" | grep -q '"cid"'; then
        echo -e "  ${YELLOW}⚠ OLD FORMAT: Pre-selected winners only (needs update)${NC}"
    else
        echo "  No vote data found"
    fi
else
    echo "  No batches available to check"
fi

echo ""
echo -e "${CYAN}═══════════════════════════════════${NC}"