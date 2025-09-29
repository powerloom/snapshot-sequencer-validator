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

# Get protocol and market from environment or use defaults
PROTOCOL_STATE=${PROTOCOL_STATE:-"0xE88E5f64AEB483d7057645326AdDFA24A3B312DF"}
DATA_MARKET=${DATA_MARKET:-"0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"}
echo -e "${GREEN}Monitoring Protocol: ${PROTOCOL_STATE:0:10}...${NC}"
echo -e "${GREEN}Monitoring Market: ${DATA_MARKET:0:10}...${NC}"
echo ""

# Get Redis port from environment or use default
REDIS_PORT=${REDIS_PORT:-6379}

# Find Redis container by inspecting which one is actually running Redis on the expected port
for container in $(docker ps --format "{{.Names}}" | grep -i redis); do
    # Check if this container is running Redis on the expected port
    PORT=$(docker inspect $container --format '{{range $p, $conf := .Config.ExposedPorts}}{{$p}}{{end}}' | grep -o '[0-9]*' | head -1)
    if [ -z "$PORT" ]; then PORT=6379; fi  # Default Redis port if not specified
    if [ "$PORT" = "$REDIS_PORT" ]; then
        REDIS_CONTAINER=$container
        break
    fi
done

# If still not found, just use the first Redis container
if [ -z "$REDIS_CONTAINER" ]; then
    REDIS_CONTAINER=$(docker ps --format "{{.Names}}" | grep -i redis | head -1)
fi

if [ -z "$REDIS_CONTAINER" ]; then
    echo -e "${RED}Error: No Redis container found on port ${REDIS_PORT}${NC}"
    echo "Make sure Redis is running and exposed on port ${REDIS_PORT}"
    echo "Start the sequencer first with: ./dsv.sh separated"
    exit 1
fi

echo -e "${GREEN}Using Redis container: $REDIS_CONTAINER${NC}"
echo ""

# Function to run Redis command
redis_cmd() {
    docker exec $REDIS_CONTAINER redis-cli "$@" 2>/dev/null
}

# CRITICAL DEBUG: Show what's actually happening
echo -e "${RED}🔍 CRITICAL: Checking actual pipeline state...${NC}"
PROCESSED_COUNT=$(redis_cmd --scan --pattern "*:*:processed:*" 2>/dev/null | wc -l)
READY_COUNT=$(redis_cmd --scan --pattern "*:*:batch:ready:*" 2>/dev/null | wc -l)
FINALIZED_COUNT=$(redis_cmd --scan --pattern "*:*:finalized:*" 2>/dev/null | wc -l)
echo "Processed submissions: $PROCESSED_COUNT | Ready batches: $READY_COUNT | Finalized: $FINALIZED_COUNT"

if [ $PROCESSED_COUNT -gt 0 ] && [ $READY_COUNT -eq 0 ]; then
    echo -e "${RED}⚠️  PROBLEM: Submissions are processed but NOT being collected into batches!${NC}"
    echo -e "${RED}   The collector component is missing or broken.${NC}"
fi
echo ""

# Active Windows (Namespaced format: protocol:market:epoch:epochID:window)
echo -e "${BLUE}🔷 Active Submission Windows:${NC}"
WINDOWS=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:epoch:*:window")
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

# Submission Queue (Namespaced)
echo -e "\n${BLUE}📥 Submission Queue:${NC}"
QUEUE_DEPTH=$(redis_cmd LLEN "${PROTOCOL_STATE}:${DATA_MARKET}:submissionQueue")
echo "  Pending: ${QUEUE_DEPTH:-0} submissions"

# Ready Batches (Namespaced format: protocol:market:batch:ready:epochID)
echo -e "\n${BLUE}📦 Ready Batches (with vote data):${NC}"
READY=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:batch:ready:*")
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

# Finalized Batches (LOCAL to this validator - what WE finalized)
echo -e "\n${BLUE}✅ LOCAL Finalized Batches (This Validator):${NC}"
# Use SCAN instead of KEYS - check for namespaced protocol:market:finalized:epochID pattern
FINALIZED=""
for pattern in "${PROTOCOL_STATE}:${DATA_MARKET}:finalized:*"; do
    SCAN_RESULT=$(redis_cmd --scan --pattern "$pattern" 2>/dev/null | head -10)
    if [ ! -z "$SCAN_RESULT" ]; then
        FINALIZED="$FINALIZED $SCAN_RESULT"
    fi
done
if [ ! -z "$FINALIZED" ]; then
    for batch in $FINALIZED; do
        EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
        # Get the finalized batch data (stored as JSON string)
        BATCH_DATA=$(redis_cmd GET "$batch")

        if [ ! -z "$BATCH_DATA" ] && command -v jq >/dev/null 2>&1; then
            # Parse JSON data with jq
            IPFS_CID=$(echo "$BATCH_DATA" | jq -r '.batchIPFSCID // .BatchIPFSCID // ""' 2>/dev/null)
            MERKLE=$(echo "$BATCH_DATA" | jq -r '.merkleRoot // .MerkleRoot // ""' 2>/dev/null | base64 2>/dev/null || echo "")
            PROJECT_COUNT=$(echo "$BATCH_DATA" | jq -r '.projectIds // .ProjectIds // [] | length' 2>/dev/null)
            TIMESTAMP=$(echo "$BATCH_DATA" | jq -r '.timestamp // .Timestamp // ""' 2>/dev/null)

            echo -e "  ${GREEN}✓ Epoch $EPOCH${NC}"
            if [ ! -z "$IPFS_CID" ] && [ "$IPFS_CID" != "null" ]; then
                echo "    📦 IPFS CID: $IPFS_CID"
            fi
            if [ ! -z "$MERKLE" ] && [ "$MERKLE" != "null" ]; then
                # If it's base64, show hex preview
                if [ ${#MERKLE} -eq 44 ]; then
                    MERKLE_HEX=$(echo "$MERKLE" | base64 -d 2>/dev/null | xxd -p -c 256 2>/dev/null | head -c 16)
                    echo "    🌳 Merkle: ${MERKLE_HEX}..."
                else
                    echo "    🌳 Merkle: ${MERKLE:0:16}..."
                fi
            fi
            if [ ! -z "$PROJECT_COUNT" ] && [ "$PROJECT_COUNT" != "null" ]; then
                echo "    📊 Projects: $PROJECT_COUNT"
            fi
            if [ ! -z "$TIMESTAMP" ] && [ "$TIMESTAMP" != "null" ] && [ "$TIMESTAMP" != "0" ]; then
                # Convert timestamp to readable format
                if command -v date >/dev/null 2>&1; then
                    FORMATTED_TIME=$(date -d "@$TIMESTAMP" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || echo "timestamp: $TIMESTAMP")
                    echo "    ⏰ Finalized: $FORMATTED_TIME"
                fi
            fi
        else
            # Fallback if no jq available
            echo "  ✓ Epoch $EPOCH (data stored)"
        fi
    done
else
    echo "  None"
fi

# LEVEL 1 AGGREGATION: Internal (Finalizer Workers → Local Complete Batch)
echo -e "\n${CYAN}═══ LEVEL 1: Internal Aggregation (Workers → Local Batch) ═══${NC}"
echo -e "${BLUE}📦 Finalizer Worker Progress:${NC}"
# Check for batch parts being collected from multiple workers (namespaced)
BATCH_PARTS=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:epoch:*:parts:*")
if [ ! -z "$BATCH_PARTS" ]; then
    for part_key in $BATCH_PARTS; do
        EPOCH=$(echo "$part_key" | sed 's/epoch://;s/:parts:.*//')
        if [[ "$part_key" == *":ready" ]]; then
            READY_COUNT=$(redis_cmd SCARD "$part_key")
            echo "  📥 Epoch $EPOCH: $READY_COUNT worker parts ready for internal aggregation"
        elif [[ "$part_key" == *":completed" ]]; then
            COMPLETED=$(redis_cmd SMEMBERS "$part_key" | wc -w)
            echo "  ✅ Epoch $EPOCH: $COMPLETED worker parts completed"
        elif [[ "$part_key" == *":total" ]]; then
            TOTAL=$(redis_cmd GET "$part_key")
            echo "  📊 Epoch $EPOCH: $TOTAL total worker parts expected"
        fi
    done
else
    echo "  No active worker aggregation"
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

# Check finalization queue (namespaced)
echo -e "\n${BLUE}🔄 Finalization Queue:${NC}"
FIN_QUEUES=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:finalizationQueue")
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

# LEVEL 2 AGGREGATION: Network-wide (Local Batch + Remote Batches → Consensus)
echo -e "\n${CYAN}═══ LEVEL 2: Network Aggregation (Validators → Consensus) ═══${NC}"
echo -e "${BLUE}🌐 Validator Network Exchange:${NC}"

# Check outgoing broadcasts queued for P2P Gateway (namespaced)
OUTGOING_QUEUE=$(redis_cmd LLEN "${PROTOCOL_STATE}:${DATA_MARKET}:outgoing:broadcast:batch")
if [ "$OUTGOING_QUEUE" -gt 0 ]; then
    echo -e "  📤 LOCAL batches queued for broadcast: $OUTGOING_QUEUE"
    echo -e "      (Aggregator broadcasts our complete local view)"
fi

# Check incoming batches from other validators (namespaced)
INCOMING_BATCHES=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:incoming:batch:*")
if [ ! -z "$INCOMING_BATCHES" ]; then
    echo -e "  ${GREEN}✓ REMOTE batches received from network:${NC}"
    VALIDATOR_COUNT=$(echo "$INCOMING_BATCHES" | sed 's/.*batch:[^:]*://' | sort -u | wc -l)
    echo -e "      Connected validators: $VALIDATOR_COUNT"
    for batch in $(echo "$INCOMING_BATCHES" | head -3); do
        # Parse epochId and validatorId from key
        EPOCH=$(echo "$batch" | sed 's/.*batch://' | cut -d: -f1)
        VALIDATOR=$(echo "$batch" | sed 's/.*batch:[^:]*://')
        echo "      📥 Epoch $EPOCH from $VALIDATOR"
    done
else
    echo -e "  ${YELLOW}⚠ No remote validator batches received yet${NC}"
fi

# Check aggregation queue (namespaced)
AGG_QUEUE=$(redis_cmd LLEN "${PROTOCOL_STATE}:${DATA_MARKET}:aggregation:queue")
if [ "$AGG_QUEUE" -gt 0 ]; then
    echo -e "  🔄 Epochs awaiting network aggregation: $AGG_QUEUE"
fi

# Check for aggregated batches (final network-wide view) - namespaced
echo -e "\n${BLUE}🎯 Network Consensus Results:${NC}"
AGGREGATED_BATCHES=$(redis_cmd KEYS "${PROTOCOL_STATE}:${DATA_MARKET}:batch:aggregated:*" | head -5)
if [ ! -z "$AGGREGATED_BATCHES" ]; then
    echo -e "  ${GREEN}✓ NETWORK-WIDE aggregated views:${NC}"
    for batch in $AGGREGATED_BATCHES; do
        EPOCH=${batch##*:}
        DATA=$(redis_cmd GET "$batch")
        if [ ! -z "$DATA" ] && command -v jq >/dev/null 2>&1; then
            PROJECTS=$(echo "$DATA" | jq -r '.ProjectVotes | length // 0' 2>/dev/null)
            CID=$(echo "$DATA" | jq -r '.BatchIPFSCID // ""' 2>/dev/null)
            # Try to determine how many validators contributed
            SUBMISSION_DETAILS=$(echo "$DATA" | jq -r '.SubmissionDetails | length // 0' 2>/dev/null)
            echo -n "    📊 Epoch $EPOCH: $PROJECTS projects (network consensus)"
            [ ! -z "$CID" ] && [ "$CID" != "" ] && echo -n " | IPFS: ${CID:0:20}..."
            echo
        else
            echo "    📊 Epoch $EPOCH (network aggregation complete)"
        fi
    done
else
    echo -e "  ${YELLOW}⚠ No network-wide aggregations completed yet${NC}"
fi

# Summary of Two-Level Aggregation Status
echo -e "\n${CYAN}═══ AGGREGATION SUMMARY ═══${NC}"
echo -e "${BLUE}📊 Two-Level Aggregation Flow:${NC}"

# Count Level 1 completions (namespaced)
LOCAL_FINALIZED_COUNT=$(redis_cmd --scan --pattern "${PROTOCOL_STATE}:${DATA_MARKET}:finalized:*" 2>/dev/null | wc -l)
TOTAL_LOCAL=$LOCAL_FINALIZED_COUNT

# Count Level 2 completions (namespaced)
NETWORK_AGGREGATED_COUNT=$(redis_cmd --scan --pattern "${PROTOCOL_STATE}:${DATA_MARKET}:batch:aggregated:*" 2>/dev/null | wc -l)

echo "  Level 1 (Workers→Local):  $TOTAL_LOCAL epochs finalized locally"
echo "  Level 2 (Validators→Net): $NETWORK_AGGREGATED_COUNT epochs aggregated network-wide"

if [ $TOTAL_LOCAL -gt 0 ] && [ $NETWORK_AGGREGATED_COUNT -eq 0 ]; then
    echo -e "\n  ${YELLOW}⚠ Local finalization working but no network aggregation yet${NC}"
    echo "      → Check if other validators are online"
    echo "      → Verify P2P gateway is broadcasting"
elif [ $TOTAL_LOCAL -eq 0 ]; then
    echo -e "\n  ${RED}⚠ No local finalization occurring${NC}"
    echo "      → Check if submissions are being processed"
    echo "      → Verify finalizer workers are running"
elif [ $NETWORK_AGGREGATED_COUNT -gt 0 ]; then
    echo -e "\n  ${GREEN}✅ Full two-level aggregation operational${NC}"
fi

echo ""
echo -e "${CYAN}═══════════════════════════════════${NC}"