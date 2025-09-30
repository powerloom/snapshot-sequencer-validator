#!/bin/bash

# Comprehensive Batch Processing Pipeline Monitor
# Shows detailed status of all pipeline stages from submission splitting to aggregation

# Color definitions
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

echo -e "${CYAN}🔍 Comprehensive Pipeline Monitor${NC}"
echo -e "${CYAN}══════════════════════════════════════════════════${NC}"
echo ""

# Function to find Redis container
find_redis_container() {
    # Method 1: Try current directory name pattern
    local dir_name=$(basename "$PWD")
    local redis_container="${dir_name}-redis-1"

    if docker ps --format "{{.Names}}" | grep -q "^${redis_container}$"; then
        echo "$redis_container"
        return 0
    fi

    # Method 2: Find by exposed Redis port (6379)
    local redis_by_port=$(docker ps --format "{{.Names}}" | while read container; do
        if docker port "$container" 2>/dev/null | grep -q "6379"; then
            echo "$container"
            return 0
        fi
    done | head -1)

    if [ ! -z "$redis_by_port" ]; then
        echo "$redis_by_port"
        return 0
    fi

    # Method 3: Find container with "redis" in name
    local redis_by_name=$(docker ps --filter "name=redis" --format "{{.Names}}" | head -1)

    if [ ! -z "$redis_by_name" ]; then
        echo "$redis_by_name"
        return 0
    fi

    return 1
}

# Find Redis container
REDIS_CONTAINER=$(find_redis_container)

if [ -z "$REDIS_CONTAINER" ]; then
    echo -e "${RED}❌ Error: Could not find Redis container${NC}"
    echo ""
    echo "Tried:"
    echo "  1. Directory-based name: $(basename "$PWD")-redis-1"
    echo "  2. Containers with port 6379 exposed"
    echo "  3. Containers with 'redis' in name"
    echo ""
    echo "Running containers:"
    docker ps --format "table {{.Names}}\t{{.Ports}}"
    exit 1
fi

echo -e "${GREEN}✅ Found Redis container: ${REDIS_CONTAINER}${NC}"
echo ""

# Get Redis port from container environment
REDIS_PORT=$(docker exec "$REDIS_CONTAINER" sh -c 'echo $REDIS_PORT' 2>/dev/null)
if [ -z "$REDIS_PORT" ]; then
    REDIS_PORT=6379
    echo -e "${YELLOW}⚠️  Could not read REDIS_PORT from container, using default: 6379${NC}"
else
    echo -e "${GREEN}✅ Redis port from container: ${REDIS_PORT}${NC}"
fi
echo ""

# Test Redis connectivity with correct port
echo "Testing Redis connectivity on port ${REDIS_PORT}..."
if ! docker exec "$REDIS_CONTAINER" sh -c "redis-cli -p ${REDIS_PORT} ping" >/dev/null 2>&1; then
    echo -e "${RED}❌ Error: Redis container found but redis-cli not responding on port ${REDIS_PORT}${NC}"
    echo ""
    echo "Debugging info:"
    echo "  Container: $REDIS_CONTAINER"
    echo "  Redis Port: ${REDIS_PORT}"
    echo "  Running: $(docker ps --filter "name=$REDIS_CONTAINER" --format "{{.Status}}")"
    echo ""
    echo "Attempting basic redis-cli command..."
    docker exec "$REDIS_CONTAINER" redis-cli --version 2>&1 || echo "redis-cli not found or not executable"
    echo ""
    echo "Checking if Redis process is running..."
    docker exec "$REDIS_CONTAINER" ps aux 2>&1 | grep redis || echo "Could not check processes"
    exit 1
fi

echo -e "${GREEN}✅ Redis connectivity confirmed on port ${REDIS_PORT}${NC}"
echo ""

# Execute comprehensive monitoring inside the Redis container with correct port
docker exec "$REDIS_CONTAINER" /bin/sh -c '
    REDIS_PORT='"${REDIS_PORT}"'

    echo "📊 Redis Connection Established"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    
    # ============= STAGE 1: SUBMISSION COLLECTION =============
    echo "📥 STAGE 1: SUBMISSION COLLECTION"
    echo "─────────────────────────────────────"
    
    # Detect key patterns first
    echo "🔍 Key Pattern Detection:"
    TOTAL_KEYS=$(redis-cli -p $REDIS_PORT DBSIZE 2>/dev/null | grep -oE "[0-9]+" || echo "0")
    echo "  Total Redis keys: $TOTAL_KEYS"

    # Sample some key patterns
    echo "  Sample key patterns:"
    redis-cli -p $REDIS_PORT KEYS "*" 2>/dev/null | head -5 | while read key; do
        echo "    - $key"
    done
    echo ""

    # Active submission windows (Check multiple patterns)
    echo "🔷 Active Submission Windows:"
    WINDOWS_FOUND=0

    # Try namespaced pattern first: {protocol}:{market}:epoch:{epochID}:window
    redis-cli -p $REDIS_PORT KEYS "*:*:epoch:*:window" 2>/dev/null | while read window_key; do
        if [ ! -z "$window_key" ]; then
            STATUS=$(redis-cli -p $REDIS_PORT GET "$window_key" 2>/dev/null)
            if [ "$STATUS" = "open" ]; then
                # Extract epoch ID from key (last segment before :window)
                EPOCH_ID=$(echo "$window_key" | sed "s/:window$//" | grep -oE "[0-9]+$")
                TTL=$(redis-cli -p $REDIS_PORT TTL "$window_key" 2>/dev/null)
                
                # Count submissions for this epoch
                SUBMISSION_COUNT=$(redis-cli -p $REDIS_PORT KEYS "*:*:epoch:$EPOCH_ID:processed" 2>/dev/null | wc -l | tr -d " ")

                echo "  ✅ Epoch: $EPOCH_ID"
                echo "     Submissions: ${SUBMISSION_COUNT:-0} | TTL: ${TTL}s | Status: COLLECTING"
                WINDOWS_FOUND=1
            fi
        fi
    done

    if [ "$WINDOWS_FOUND" -eq 0 ]; then
        echo "  ⚫ No active windows"
    fi

    # Submission queue depth (check both namespaced and non-namespaced)
    echo ""
    echo "📊 Submission Queue:"
    QUEUE_DEPTH=$(redis-cli -p $REDIS_PORT LLEN "submissionQueue" 2>/dev/null || echo "0")
    NAMESPACED_QUEUES=$(redis-cli -p $REDIS_PORT KEYS "*:*:submissionQueue" 2>/dev/null)

    if [ ! -z "$NAMESPACED_QUEUES" ]; then
        echo "$NAMESPACED_QUEUES" | while read queue_key; do
            if [ ! -z "$queue_key" ]; then
                DEPTH=$(redis-cli -p $REDIS_PORT LLEN "$queue_key" 2>/dev/null)
                if [ ! -z "$DEPTH" ] && [ "$DEPTH" -gt 0 ]; then
                    echo "  🔸 $queue_key: $DEPTH submissions"
                    QUEUE_DEPTH=$((QUEUE_DEPTH + DEPTH))
                fi
            fi
        done
    fi

    if [ "$QUEUE_DEPTH" -gt 0 ]; then
        if [ "$QUEUE_DEPTH" -gt 100 ]; then
            echo "  ⚠️  WARNING: Queue backlog detected!"
        fi
    else
        echo "  ✓ All queues empty"
    fi
    
    # Processed submissions by project (vote tracking)
    echo ""
    echo "🗳️ Vote Distribution (per project):"
    VOTE_KEYS=$(redis-cli -p $REDIS_PORT KEYS "*:*:epoch:*:project:*:votes" 2>/dev/null | head -5)
    if [ ! -z "$VOTE_KEYS" ]; then
        echo "$VOTE_KEYS" | while read vote_key; do
            if [ ! -z "$vote_key" ]; then
                PROJECT=$(echo "$vote_key" | grep -oE "project:[^:]+:" | sed "s/project://g" | sed "s/://g")
                VOTE_COUNT=$(redis-cli -p $REDIS_PORT HLEN "$vote_key" 2>/dev/null)
                echo "  📊 Project $PROJECT: $VOTE_COUNT unique CID votes"
            fi
        done
    else
        echo "  ⚫ No vote data yet"
    fi
    
    echo ""
    # ============= STAGE 2: BATCH SPLITTING =============
    echo "🔀 STAGE 2: BATCH SPLITTING (Window Close → Parallel Batches)"
    echo "─────────────────────────────────────"
    
    # Ready batches instead of batch metadata (actual data structure)
    echo "📦 Ready Batches (collected submissions):"
    BATCH_META_KEYS=$(redis-cli -p $REDIS_PORT KEYS "*:*:batch:ready:*" 2>/dev/null | head -5)
    if [ ! -z "$BATCH_META_KEYS" ]; then
        echo "$BATCH_META_KEYS" | while read meta_key; do
            if [ ! -z "$meta_key" ]; then
                # Extract epoch from key format: protocol:market:batch:ready:epochID
                EPOCH_ID=$(echo "$meta_key" | grep -oE "[0-9]+$")
                META_DATA=$(redis-cli -p $REDIS_PORT GET "$meta_key" 2>/dev/null)
                if [ ! -z "$META_DATA" ]; then
                    # Count actual projects - check if jq is available
                    if command -v jq >/dev/null 2>&1; then
                        PROJECT_COUNT=$(echo "$META_DATA" | jq 'keys | length' 2>/dev/null || echo "0")
                    else
                        # Fallback to simple grep count if jq not available
                        PROJECT_COUNT=$(echo "$META_DATA" | grep -o '"[^"]*":{' | wc -l)
                    fi
                    HAS_VOTES=$(echo "$META_DATA" | grep -q '"cid_votes"' && echo "with vote data" || echo "pre-selected")
                    
                    echo "  📋 Epoch $EPOCH_ID:"
                    echo "     Projects collected: $PROJECT_COUNT"
                    echo "     Format: $HAS_VOTES"
                    echo "     Status: READY FOR FINALIZATION"
                fi
            fi
        done
    else
        echo "  ⚫ No ready batches found"
    fi
    
    # Finalization queue status (Updated format: protocol:market:finalizationQueue)
    echo ""
    echo "⏳ Finalization Queue:"
    # Look for finalization queues with pattern protocol:market:finalizationQueue
    FIN_QUEUES=$(redis-cli -p $REDIS_PORT KEYS "*:*:finalizationQueue" 2>/dev/null)
    TOTAL_BATCHES=0
    if [ ! -z "$FIN_QUEUES" ]; then
        echo "$FIN_QUEUES" | while read queue_key; do
            if [ ! -z "$queue_key" ]; then
                MARKET=$(echo "$queue_key" | sed "s/:finalizationQueue$//" | sed "s/^[^:]*://")
                QUEUE_DEPTH=$(redis-cli -p $REDIS_PORT LLEN "$queue_key" 2>/dev/null)
                echo "  📦 Market $MARKET: $QUEUE_DEPTH batches waiting"
                TOTAL_BATCHES=$((TOTAL_BATCHES + QUEUE_DEPTH))
                
                # Show details of first few batches for this market
                if [ "$QUEUE_DEPTH" -gt 0 ]; then
                    echo "  📋 Next batches in $MARKET queue:"
                    for i in 0 1; do
                        BATCH_DATA=$(redis-cli -p $REDIS_PORT LINDEX "$queue_key" $i 2>/dev/null)
                        if [ ! -z "$BATCH_DATA" ]; then
                            BATCH_EPOCH=$(echo "$BATCH_DATA" | grep -o "\"epoch_id\":\"[^\"]*" | cut -d"\"" -f4)
                            BATCH_ID=$(echo "$BATCH_DATA" | grep -o "\"batch_id\":[0-9]*" | cut -d: -f2)
                            echo "     [$((i+1))] Epoch $BATCH_EPOCH, Batch #$BATCH_ID"
                        fi
                    done
                fi
            fi
        done
    else
        echo "  ✓ No finalization queues found"
    fi
    
    echo ""
    # ============= STAGE 3: PARALLEL FINALIZATION =============
    echo "⚡ STAGE 3: PARALLEL FINALIZATION WORKERS"
    echo "─────────────────────────────────────"
    
    # Worker status tracking
    echo "👷 Finalizer Workers:"
    WORKER_KEYS=$(redis-cli -p $REDIS_PORT KEYS "worker:finalizer:*:status" 2>/dev/null)
    if [ ! -z "$WORKER_KEYS" ]; then
        ACTIVE_COUNT=0
        IDLE_COUNT=0
        echo "$WORKER_KEYS" | while read worker_key; do
            if [ ! -z "$worker_key" ]; then
                WORKER_ID=$(echo "$worker_key" | grep -oE "finalizer:[0-9]+" | cut -d: -f2)
                STATUS=$(redis-cli -p $REDIS_PORT GET "$worker_key" 2>/dev/null)
                HEARTBEAT_KEY=$(echo "$worker_key" | sed "s/:status/:heartbeat/")
                HEARTBEAT=$(redis-cli -p $REDIS_PORT GET "$HEARTBEAT_KEY" 2>/dev/null)
                
                # Check if heartbeat is recent (within 60 seconds)
                CURRENT_TIME=$(date +%s)
                if [ ! -z "$HEARTBEAT" ]; then
                    TIME_DIFF=$((CURRENT_TIME - HEARTBEAT))
                    if [ "$TIME_DIFF" -lt 60 ]; then
                        HEALTH="✅ Healthy"
                    else
                        HEALTH="⚠️ Stale (${TIME_DIFF}s ago)"
                    fi
                else
                    HEALTH="❌ No heartbeat"
                fi
                
                # Get current batch if processing
                if [ "$STATUS" = "processing" ]; then
                    BATCH_KEY=$(echo "$worker_key" | sed "s/:status/:current_batch/")
                    CURRENT_BATCH=$(redis-cli -p $REDIS_PORT GET "$BATCH_KEY" 2>/dev/null)
                    echo "  Worker #$WORKER_ID: 🔄 PROCESSING - $CURRENT_BATCH | $HEALTH"
                    ACTIVE_COUNT=$((ACTIVE_COUNT + 1))
                else
                    echo "  Worker #$WORKER_ID: ⏸️ IDLE | $HEALTH"
                    IDLE_COUNT=$((IDLE_COUNT + 1))
                fi
            fi
        done
        echo ""
        echo "  📊 Summary: $ACTIVE_COUNT active, $IDLE_COUNT idle"
    else
        echo "  ⚫ No workers registered (TODO: Implement parallel workers)"
    fi
    
    # Batch parts being processed
    echo ""
    echo "🔧 Batch Parts Status:"
    BATCH_PART_KEYS=$(redis-cli -p $REDIS_PORT KEYS "batch:*:part:*:status" 2>/dev/null | head -10)
    if [ ! -z "$BATCH_PART_KEYS" ]; then
        COMPLETED=0
        PROCESSING=0
        PENDING=0
        
        echo "$BATCH_PART_KEYS" | while read part_key; do
            if [ ! -z "$part_key" ]; then
                STATUS=$(redis-cli -p $REDIS_PORT GET "$part_key" 2>/dev/null)
                case "$STATUS" in
                    "completed") COMPLETED=$((COMPLETED + 1)) ;;
                    "processing") PROCESSING=$((PROCESSING + 1)) ;;
                    "pending") PENDING=$((PENDING + 1)) ;;
                esac
            fi
        done
        
        echo "  ✅ Completed: $COMPLETED"
        echo "  🔄 Processing: $PROCESSING"
        echo "  ⏳ Pending: $PENDING"
    else
        echo "  ⚫ No batch parts tracked yet"
    fi
    
    echo ""
    # ============= STAGE 4: AGGREGATION =============
    echo "🔗 STAGE 4: AGGREGATION WORKER"
    echo "─────────────────────────────────────"
    
    # Aggregation queue
    echo "📥 Aggregation Queue:"
    AGG_QUEUE_DEPTH=$(redis-cli -p $REDIS_PORT LLEN "aggregationQueue" 2>/dev/null)
    if [ ! -z "$AGG_QUEUE_DEPTH" ] && [ "$AGG_QUEUE_DEPTH" -gt 0 ]; then
        echo "  📦 Epochs awaiting aggregation: $AGG_QUEUE_DEPTH"
    else
        echo "  ✓ No epochs pending aggregation"
    fi
    
    # Epochs ready for aggregation (all parts complete)
    echo ""
    echo "🎯 Epochs Ready for Aggregation:"
    READY_EPOCHS=$(redis-cli -p $REDIS_PORT KEYS "epoch:*:parts:ready" 2>/dev/null)
    if [ ! -z "$READY_EPOCHS" ]; then
        echo "$READY_EPOCHS" | while read ready_key; do
            if [ ! -z "$ready_key" ]; then
                EPOCH_ID=$(echo "$ready_key" | grep -oE "epoch:[0-9]+" | cut -d: -f2)
                PARTS_COMPLETE=$(redis-cli -p $REDIS_PORT GET "epoch:$EPOCH_ID:parts:completed" 2>/dev/null)
                PARTS_TOTAL=$(redis-cli -p $REDIS_PORT GET "epoch:$EPOCH_ID:parts:total" 2>/dev/null)
                
                if [ "$PARTS_COMPLETE" = "$PARTS_TOTAL" ]; then
                    echo "  ✅ Epoch $EPOCH_ID: ALL $PARTS_TOTAL parts complete - READY"
                else
                    echo "  ⏳ Epoch $EPOCH_ID: $PARTS_COMPLETE/$PARTS_TOTAL parts - WAITING"
                fi
            fi
        done
    else
        echo "  ⚫ No epochs ready for aggregation"
    fi
    
    # Aggregation worker status
    echo ""
    echo "👷 Aggregation Worker:"
    AGG_STATUS=$(redis-cli -p $REDIS_PORT GET "worker:aggregator:status" 2>/dev/null)
    AGG_HEARTBEAT=$(redis-cli -p $REDIS_PORT GET "worker:aggregator:heartbeat" 2>/dev/null)
    
    if [ ! -z "$AGG_STATUS" ]; then
        CURRENT_TIME=$(date +%s)
        if [ ! -z "$AGG_HEARTBEAT" ]; then
            TIME_DIFF=$((CURRENT_TIME - AGG_HEARTBEAT))
            if [ "$TIME_DIFF" -lt 60 ]; then
                HEALTH="✅ Healthy"
            else
                HEALTH="⚠️ Stale (${TIME_DIFF}s ago)"
            fi
        else
            HEALTH="❌ No heartbeat"
        fi
        
        if [ "$AGG_STATUS" = "processing" ]; then
            CURRENT_EPOCH=$(redis-cli -p $REDIS_PORT GET "worker:aggregator:current_epoch" 2>/dev/null)
            echo "  Status: 🔄 PROCESSING epoch $CURRENT_EPOCH | $HEALTH"
        else
            echo "  Status: ⏸️ IDLE | $HEALTH"
        fi
        
        # Show what aggregator is waiting for
        BLOCKING_PARTS=$(redis-cli -p $REDIS_PORT KEYS "batch:*:part:*:processing" 2>/dev/null | wc -l)
        if [ "$BLOCKING_PARTS" -gt 0 ]; then
            echo "  ⏳ Waiting for: $BLOCKING_PARTS batch parts to complete"
        fi
    else
        echo "  ⚫ Aggregator not running (TODO: Implement aggregation worker)"
    fi
    
    echo ""
    # ============= STAGE 5: FINAL OUTPUT =============
    echo "📤 STAGE 5: FINAL OUTPUT (IPFS + Validator Votes)"
    echo "─────────────────────────────────────"
    
    # Finalized batches (check both namespaced and legacy patterns)
    echo "✅ Finalized Batches:"
    FINALIZED_NAMESPACED=$(redis-cli -p $REDIS_PORT KEYS "*:*:finalized:*" 2>/dev/null | head -5)
    FINALIZED_LEGACY=$(redis-cli -p $REDIS_PORT KEYS "batch:finalized:*" 2>/dev/null | head -5)
    FINALIZED="$FINALIZED_NAMESPACED $FINALIZED_LEGACY"

    if [ ! -z "$FINALIZED" ] && [ "$FINALIZED" != " " ]; then
        echo "$FINALIZED" | tr ' ' '\n' | while read final_key; do
            if [ ! -z "$final_key" ]; then
                EPOCH_ID=$(echo "$final_key" | grep -oE "[0-9]+$")
                # Try to get as JSON first (new format)
                BATCH_DATA=$(redis-cli -p $REDIS_PORT GET "$final_key" 2>/dev/null)
                if [ ! -z "$BATCH_DATA" ]; then
                    # Try to extract IPFS CID from JSON
                    if command -v jq >/dev/null 2>&1; then
                        IPFS_CID=$(echo "$BATCH_DATA" | jq -r '.batch_ipfs_cid // .BatchIPFSCID // empty' 2>/dev/null)
                        PROJECT_COUNT=$(echo "$BATCH_DATA" | jq '.project_ids | length' 2>/dev/null || echo "?")
                    else
                        IPFS_CID=$(echo "$BATCH_DATA" | grep -o '"batch_ipfs_cid":"[^"]*"' | cut -d'"' -f4)
                        PROJECT_COUNT="?"
                    fi

                    echo "  📦 Epoch $EPOCH_ID:"
                    echo "     Projects: $PROJECT_COUNT"
                    echo "     IPFS: ${IPFS_CID:-pending}"
                else
                    # Legacy hash format
                    IPFS_CID=$(redis-cli -p $REDIS_PORT HGET "$final_key" "ipfs_cid" 2>/dev/null)
                    echo "  📦 Epoch $EPOCH_ID (legacy hash):"
                    echo "     IPFS: ${IPFS_CID:-pending}"
                fi
            fi
        done
    else
        echo "  ⚫ No finalized batches yet"
    fi
    
    # Aggregated batches (network-wide consensus)
    echo ""
    echo "🌐 Aggregated Batches (Network Consensus):"
    AGGREGATED=$(redis-cli -p $REDIS_PORT KEYS "*:*:batch:aggregated:*" 2>/dev/null | head -5)
    if [ ! -z "$AGGREGATED" ]; then
        AGG_COUNT=$(echo "$AGGREGATED" | wc -l | tr -d " ")
        echo "  ✅ $AGG_COUNT network-aggregated epochs found"
        echo "$AGGREGATED" | while read agg_key; do
            if [ ! -z "$agg_key" ]; then
                EPOCH_ID=$(echo "$agg_key" | grep -oE "[0-9]+$")
                BATCH_DATA=$(redis-cli -p $REDIS_PORT GET "$agg_key" 2>/dev/null)
                if [ ! -z "$BATCH_DATA" ] && command -v jq >/dev/null 2>&1; then
                    VALIDATORS=$(echo "$BATCH_DATA" | jq '.validator_count // 1' 2>/dev/null)
                    PROJECTS=$(echo "$BATCH_DATA" | jq '.project_votes | length' 2>/dev/null)
                    echo "  📦 Epoch $EPOCH_ID: $VALIDATORS validators, $PROJECTS projects"
                else
                    echo "  📦 Epoch $EPOCH_ID"
                fi
            fi
        done
    else
        echo "  ⚫ No aggregated batches yet"
    fi

    # Validator votes broadcast status
    echo ""
    echo "🗳️ Validator Votes Broadcast:"
    VOTES_BROADCAST=$(redis-cli -p $REDIS_PORT GET "validator:votes:last_broadcast" 2>/dev/null)
    if [ ! -z "$VOTES_BROADCAST" ]; then
        echo "  Last broadcast: $VOTES_BROADCAST"
    else
        echo "  ⚫ No votes broadcast yet"
    fi
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # ============= PERFORMANCE METRICS =============
    echo "📊 PERFORMANCE METRICS"
    echo "─────────────────────────────────────"
    
    # Calculate throughput
    TOTAL_PROCESSED=$(redis-cli -p $REDIS_PORT GET "metrics:total_processed" 2>/dev/null)
    PROCESSING_RATE=$(redis-cli -p $REDIS_PORT GET "metrics:processing_rate" 2>/dev/null)
    AVG_LATENCY=$(redis-cli -p $REDIS_PORT GET "metrics:avg_latency" 2>/dev/null)
    
    echo "  Total Processed: ${TOTAL_PROCESSED:-0} submissions"
    echo "  Processing Rate: ${PROCESSING_RATE:-0} sub/min"
    echo "  Avg Latency: ${AVG_LATENCY:-N/A} ms"
    
    # Pipeline bottlenecks
    echo ""
    echo "⚠️ Potential Bottlenecks:"
    if [ "${QUEUE_DEPTH:-0}" -gt 100 ]; then
        echo "  🔴 Submission queue backlog ($QUEUE_DEPTH pending)"
    fi
    if [ "${FIN_QUEUE_DEPTH:-0}" -gt 10 ]; then
        echo "  🔴 Finalization queue backlog ($FIN_QUEUE_DEPTH batches)"
    fi
    if [ "${AGG_QUEUE_DEPTH:-0}" -gt 5 ]; then
        echo "  🔴 Aggregation queue backlog ($AGG_QUEUE_DEPTH epochs)"
    fi
    
    # All clear message
    if [ "${QUEUE_DEPTH:-0}" -le 10 ] && [ "${FIN_QUEUE_DEPTH:-0}" -le 5 ] && [ "${AGG_QUEUE_DEPTH:-0}" -le 2 ]; then
        echo "  ✅ Pipeline flowing smoothly"
    fi
'