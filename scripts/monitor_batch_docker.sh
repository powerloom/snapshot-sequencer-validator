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
    
    # Check current submission window
    CURRENT_WINDOW=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "submission_window:current" 2>/dev/null)
    if [ ! -z "$CURRENT_WINDOW" ]; then
        echo "📊 Current Window: $CURRENT_WINDOW"
    fi
    
    # Check ready batches
    echo -e "\n📦 Ready Batches:"
    redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:ready:*" 2>/dev/null | while read batch; do
        if [ ! -z "$batch" ]; then
            echo "  - $batch"
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