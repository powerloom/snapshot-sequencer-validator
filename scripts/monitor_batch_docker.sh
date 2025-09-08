#!/bin/bash

# Monitor batch status from inside Docker container
# Usage: ./monitor_batch_docker.sh [container_name]

CONTAINER="${1:-decentralized-sequencer-sequencer-custom-1}"

echo "🔍 Monitoring Batch Status in Container: $CONTAINER"
echo "=================================="

# Execute the monitoring script inside the container
docker exec -it $CONTAINER /bin/sh -c '
    REDIS_HOST="${REDIS_HOST:-redis}"
    REDIS_PORT="${REDIS_PORT:-6379}"
    
    echo "📊 Checking Redis at $REDIS_HOST:$REDIS_PORT"
    echo ""
    
    # Check active submission windows (Updated format: epoch:market:epochID:window)
    echo "🔷 Current Submission Window:"
    WINDOWS_FOUND=0
    redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "epoch:*:*:window" 2>/dev/null | while read window_key; do
        if [ ! -z "$window_key" ]; then
            STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$window_key" 2>/dev/null)
            if [ "$STATUS" = "open" ]; then
                # Parse epoch:market:epochID:window format
                MARKET=$(echo "$window_key" | sed "s/^epoch://;s/:.*:window$//" | cut -d: -f1)
                EPOCH=$(echo "$window_key" | sed "s/^epoch:[^:]*://;s/:window$//")
                TTL=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT TTL "$window_key" 2>/dev/null)
                echo "  ✅ Market: $MARKET, Epoch: $EPOCH (TTL: ${TTL}s)"
                WINDOWS_FOUND=1
            fi
        fi
    done
    
    if [ "$WINDOWS_FOUND" -eq 0 ]; then
        echo "  None active"
    fi
    
    # Check ready batches (Updated format: protocol:market:batch:ready:epochID)
    echo -e "\n📦 Ready Batches:"
    redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "*:*:batch:ready:*" 2>/dev/null | while read batch; do
        if [ ! -z "$batch" ]; then
            # Extract protocol:market and epoch from protocol:market:batch:ready:epochID
            PROTOCOL_MARKET=$(echo "$batch" | sed "s/:batch:ready:.*//")
            EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
            echo "  - $PROTOCOL_MARKET - Epoch $EPOCH"
            BATCH_SIZE=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$batch" 2>/dev/null | wc -c)
            echo "    Size: ~$BATCH_SIZE bytes"
        fi
    done
    
    # Check pending submissions
    echo -e "\n⏳ Pending Submissions:"
    PENDING_COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "submissions:pending" 2>/dev/null)
    if [ ! -z "$PENDING_COUNT" ] && [ "$PENDING_COUNT" != "0" ]; then
        echo "  Count: $PENDING_COUNT"
    else
        echo "  None"
    fi
    
    # Check window submission counts
    echo -e "\n📈 Window Statistics:"
    redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "window:*:submissions" 2>/dev/null | while read window; do
        if [ ! -z "$window" ]; then
            WINDOW_ID=$(echo $window | sed "s/window://g" | sed "s/:submissions//g")
            COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "$window" 2>/dev/null)
            echo "  Window $WINDOW_ID: $COUNT submissions"
        fi
    done
    
    # Check batch preparation status
    echo -e "\n🎯 Batch Preparation Status:"
    PREP_STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "batch:preparation:status" 2>/dev/null)
    if [ ! -z "$PREP_STATUS" ]; then
        echo "  Status: $PREP_STATUS"
    else
        echo "  No active preparation"
    fi
'