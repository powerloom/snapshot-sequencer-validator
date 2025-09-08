#!/bin/bash

# Comprehensive Batch Processing Pipeline Monitor
# Shows detailed status of all pipeline stages from submission splitting to aggregation

# Accept container name as parameter, or try to auto-detect
CONTAINER="$1"

if [ -z "$CONTAINER" ]; then
    # Try to auto-detect container
    CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
    if [ -z "$CONTAINER" ]; then
        CONTAINER=$(docker ps --filter "name=listener" --format "{{.Names}}" | head -1)
    fi
    if [ -z "$CONTAINER" ]; then
        CONTAINER=$(docker ps --filter "name=dequeuer" --format "{{.Names}}" | head -1)
    fi
    
    if [ -z "$CONTAINER" ]; then
        echo "Error: No running sequencer containers found"
        echo "Usage: $0 [container_name]"
        echo "Or start the sequencer first with: ./launch.sh sequencer or ./launch.sh distributed"
        exit 1
    fi
fi

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

# Execute comprehensive monitoring inside the container
docker exec -it $CONTAINER /bin/sh -c '
    REDIS_HOST="${REDIS_HOST:-redis}"
    REDIS_PORT="${REDIS_PORT:-6379}"
    
    echo "📊 Redis: $REDIS_HOST:$REDIS_PORT"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    
    # ============= STAGE 1: SUBMISSION COLLECTION =============
    echo "📥 STAGE 1: SUBMISSION COLLECTION"
    echo "─────────────────────────────────────"
    
    # Active submission windows
    echo "🔷 Active Submission Windows:"
    WINDOWS_FOUND=0
    redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "epoch:*:window" 2>/dev/null | while read window_key; do
        if [ ! -z "$window_key" ]; then
            STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$window_key" 2>/dev/null)
            if [ "$STATUS" = "open" ]; then
                EPOCH_INFO=$(echo "$window_key" | sed "s/^epoch://;s/:window$//")
                TTL=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT TTL "$window_key" 2>/dev/null)
                
                # Count submissions for this epoch
                SUBMISSION_COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT SCARD "powerloom-localnet:eth:epoch:${EPOCH_INFO##*:}:processed" 2>/dev/null)
                
                echo "  ✅ Epoch: $EPOCH_INFO"
                echo "     Submissions: ${SUBMISSION_COUNT:-0} | TTL: ${TTL}s | Status: COLLECTING"
                WINDOWS_FOUND=1
            fi
        fi
    done
    
    if [ "$WINDOWS_FOUND" -eq 0 ]; then
        echo "  ⚫ No active windows"
    fi
    
    # Submission queue depth
    echo ""
    echo "📊 Submission Queue:"
    QUEUE_DEPTH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "submissionQueue" 2>/dev/null)
    if [ ! -z "$QUEUE_DEPTH" ] && [ "$QUEUE_DEPTH" -gt 0 ]; then
        echo "  🔸 Pending: $QUEUE_DEPTH submissions"
        if [ "$QUEUE_DEPTH" -gt 100 ]; then
            echo "  ⚠️  WARNING: Queue backlog detected!"
        fi
    else
        echo "  ✓ Queue empty"
    fi
    
    # Processed submissions by project (vote tracking)
    echo ""
    echo "🗳️ Vote Distribution (per project):"
    VOTE_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "powerloom-localnet:eth:epoch:*:project:*:votes" 2>/dev/null | head -5)
    if [ ! -z "$VOTE_KEYS" ]; then
        echo "$VOTE_KEYS" | while read vote_key; do
            if [ ! -z "$vote_key" ]; then
                PROJECT=$(echo "$vote_key" | grep -oE "project:[^:]+:" | sed "s/project://g" | sed "s/://g")
                VOTES=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT HGETALL "$vote_key" 2>/dev/null)
                echo "  📊 Project $PROJECT: Multiple CIDs with votes"
            fi
        done
    else
        echo "  ⚫ No vote data yet"
    fi
    
    echo ""
    # ============= STAGE 2: BATCH SPLITTING =============
    echo "🔀 STAGE 2: BATCH SPLITTING (Window Close → Parallel Batches)"
    echo "─────────────────────────────────────"
    
    # Batch metadata (shows how epochs were split)
    echo "📦 Split Batch Metadata:"
    BATCH_META_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "*:epoch:*:batch:meta" 2>/dev/null | head -5)
    if [ ! -z "$BATCH_META_KEYS" ]; then
        echo "$BATCH_META_KEYS" | while read meta_key; do
            if [ ! -z "$meta_key" ]; then
                META_DATA=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$meta_key" 2>/dev/null)
                if [ ! -z "$META_DATA" ]; then
                    # Parse JSON manually (basic extraction)
                    EPOCH_ID=$(echo "$META_DATA" | grep -o "\"epoch_id\":\"[^\"]*" | cut -d"\"" -f4)
                    TOTAL_BATCHES=$(echo "$META_DATA" | grep -o "\"total_batches\":[0-9]*" | cut -d: -f2)
                    TOTAL_PROJECTS=$(echo "$META_DATA" | grep -o "\"total_projects\":[0-9]*" | cut -d: -f2)
                    
                    echo "  📋 Epoch $EPOCH_ID:"
                    echo "     Split into: $TOTAL_BATCHES batches"
                    echo "     Total projects: $TOTAL_PROJECTS"
                    echo "     Status: READY FOR FINALIZATION"
                fi
            fi
        done
    else
        echo "  ⚫ No batches split yet"
    fi
    
    # Finalization queue status
    echo ""
    echo "⏳ Finalization Queue:"
    FIN_QUEUE_DEPTH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "powerloom-localnet:eth:finalizationQueue" 2>/dev/null)
    if [ ! -z "$FIN_QUEUE_DEPTH" ] && [ "$FIN_QUEUE_DEPTH" -gt 0 ]; then
        echo "  📦 Batches waiting: $FIN_QUEUE_DEPTH"
        
        # Show details of first few batches
        echo "  📋 Next batches in queue:"
        for i in 0 1 2; do
            BATCH_DATA=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LINDEX "powerloom-localnet:eth:finalizationQueue" $i 2>/dev/null)
            if [ ! -z "$BATCH_DATA" ]; then
                BATCH_EPOCH=$(echo "$BATCH_DATA" | grep -o "\"epoch_id\":\"[^\"]*" | cut -d"\"" -f4)
                BATCH_ID=$(echo "$BATCH_DATA" | grep -o "\"batch_id\":[0-9]*" | cut -d: -f2)
                echo "     [$((i+1))] Epoch $BATCH_EPOCH, Batch #$BATCH_ID"
            fi
        done
    else
        echo "  ✓ Queue empty (no batches pending)"
    fi
    
    echo ""
    # ============= STAGE 3: PARALLEL FINALIZATION =============
    echo "⚡ STAGE 3: PARALLEL FINALIZATION WORKERS"
    echo "─────────────────────────────────────"
    
    # Worker status tracking
    echo "👷 Finalizer Workers:"
    WORKER_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "worker:finalizer:*:status" 2>/dev/null)
    if [ ! -z "$WORKER_KEYS" ]; then
        ACTIVE_COUNT=0
        IDLE_COUNT=0
        echo "$WORKER_KEYS" | while read worker_key; do
            if [ ! -z "$worker_key" ]; then
                WORKER_ID=$(echo "$worker_key" | grep -oE "finalizer:[0-9]+" | cut -d: -f2)
                STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$worker_key" 2>/dev/null)
                HEARTBEAT_KEY=$(echo "$worker_key" | sed "s/:status/:heartbeat/")
                HEARTBEAT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$HEARTBEAT_KEY" 2>/dev/null)
                
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
                    CURRENT_BATCH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$BATCH_KEY" 2>/dev/null)
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
    BATCH_PART_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:*:part:*:status" 2>/dev/null | head -10)
    if [ ! -z "$BATCH_PART_KEYS" ]; then
        COMPLETED=0
        PROCESSING=0
        PENDING=0
        
        echo "$BATCH_PART_KEYS" | while read part_key; do
            if [ ! -z "$part_key" ]; then
                STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$part_key" 2>/dev/null)
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
    AGG_QUEUE_DEPTH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "aggregationQueue" 2>/dev/null)
    if [ ! -z "$AGG_QUEUE_DEPTH" ] && [ "$AGG_QUEUE_DEPTH" -gt 0 ]; then
        echo "  📦 Epochs awaiting aggregation: $AGG_QUEUE_DEPTH"
    else
        echo "  ✓ No epochs pending aggregation"
    fi
    
    # Epochs ready for aggregation (all parts complete)
    echo ""
    echo "🎯 Epochs Ready for Aggregation:"
    READY_EPOCHS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "epoch:*:parts:ready" 2>/dev/null)
    if [ ! -z "$READY_EPOCHS" ]; then
        echo "$READY_EPOCHS" | while read ready_key; do
            if [ ! -z "$ready_key" ]; then
                EPOCH_ID=$(echo "$ready_key" | grep -oE "epoch:[0-9]+" | cut -d: -f2)
                PARTS_COMPLETE=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "epoch:$EPOCH_ID:parts:completed" 2>/dev/null)
                PARTS_TOTAL=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "epoch:$EPOCH_ID:parts:total" 2>/dev/null)
                
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
    AGG_STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "worker:aggregator:status" 2>/dev/null)
    AGG_HEARTBEAT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "worker:aggregator:heartbeat" 2>/dev/null)
    
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
            CURRENT_EPOCH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "worker:aggregator:current_epoch" 2>/dev/null)
            echo "  Status: 🔄 PROCESSING epoch $CURRENT_EPOCH | $HEALTH"
        else
            echo "  Status: ⏸️ IDLE | $HEALTH"
        fi
        
        # Show what aggregator is waiting for
        BLOCKING_PARTS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:*:part:*:processing" 2>/dev/null | wc -l)
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
    
    # Finalized batches
    echo "✅ Finalized Batches:"
    FINALIZED=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:finalized:*" 2>/dev/null | head -5)
    if [ ! -z "$FINALIZED" ]; then
        echo "$FINALIZED" | while read final_key; do
            if [ ! -z "$final_key" ]; then
                EPOCH_ID=$(echo "$final_key" | grep -oE "[0-9]+$")
                IPFS_CID=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT HGET "$final_key" "ipfs_cid" 2>/dev/null)
                MERKLE_ROOT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT HGET "$final_key" "merkle_root" 2>/dev/null)
                
                echo "  📦 Epoch $EPOCH_ID:"
                echo "     IPFS: ${IPFS_CID:-pending}"
                echo "     Merkle: ${MERKLE_ROOT:0:16}..."
            fi
        done
    else
        echo "  ⚫ No finalized batches yet"
    fi
    
    # Validator votes broadcast status
    echo ""
    echo "🗳️ Validator Votes Broadcast:"
    VOTES_BROADCAST=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "validator:votes:last_broadcast" 2>/dev/null)
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
    TOTAL_PROCESSED=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "metrics:total_processed" 2>/dev/null)
    PROCESSING_RATE=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "metrics:processing_rate" 2>/dev/null)
    AVG_LATENCY=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "metrics:avg_latency" 2>/dev/null)
    
    echo "  Total Processed: ${TOTAL_PROCESSED:-0} submissions"
    echo "  Processing Rate: ${PROCESSING_RATE:-0} sub/min"
    echo "  Avg Latency: ${AVG_LATENCY:-N/A} ms"
    
    # Pipeline bottlenecks
    echo ""
    echo "⚠️ Potential Bottlenecks:"
    if [ "$QUEUE_DEPTH" -gt 100 ]; then
        echo "  🔴 Submission queue backlog ($QUEUE_DEPTH pending)"
    fi
    if [ "$FIN_QUEUE_DEPTH" -gt 10 ]; then
        echo "  🔴 Finalization queue backlog ($FIN_QUEUE_DEPTH batches)"
    fi
    if [ "$AGG_QUEUE_DEPTH" -gt 5 ]; then
        echo "  🔴 Aggregation queue backlog ($AGG_QUEUE_DEPTH epochs)"
    fi
    
    # All clear message
    if [ "$QUEUE_DEPTH" -le 10 ] && [ "$FIN_QUEUE_DEPTH" -le 5 ] && [ "$AGG_QUEUE_DEPTH" -le 2 ]; then
        echo "  ✅ Pipeline flowing smoothly"
    fi
'