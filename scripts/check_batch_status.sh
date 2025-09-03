#!/bin/bash

# Redis connection details
REDIS_HOST="${REDIS_HOST:-localhost}"
REDIS_PORT="${REDIS_PORT:-6379}"

echo "🔍 Checking Batch Preparation Status..."
echo "=================================="

# Check current submission window
CURRENT_WINDOW=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "submission_window:current" 2>/dev/null)
if [ ! -z "$CURRENT_WINDOW" ]; then
    echo "📊 Current Window: $CURRENT_WINDOW"
fi

# Check if there are any ready batches
echo -e "\n📦 Ready Batches:"
READY_BATCHES=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:ready:*" 2>/dev/null)
if [ ! -z "$READY_BATCHES" ]; then
    for batch in $READY_BATCHES; do
        echo "  - $batch"
        # Get batch details
        BATCH_DATA=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$batch" 2>/dev/null)
        if [ ! -z "$BATCH_DATA" ]; then
            echo "    Size: $(echo $BATCH_DATA | jq -r '.submissions | length' 2>/dev/null) submissions"
            echo "    Window: $(echo $BATCH_DATA | jq -r '.windowId' 2>/dev/null)"
        fi
    done
else
    echo "  None found"
fi

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
WINDOW_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "window:*:submissions" 2>/dev/null)
if [ ! -z "$WINDOW_KEYS" ]; then
    for window in $WINDOW_KEYS; do
        WINDOW_ID=$(echo $window | sed 's/window://g' | sed 's/:submissions//g')
        COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "$window" 2>/dev/null)
        echo "  Window $WINDOW_ID: $COUNT submissions"
    done
else
    echo "  No window data found"
fi

# Check last batch preparation time
LAST_BATCH_TIME=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "batch:last_prepared_time" 2>/dev/null)
if [ ! -z "$LAST_BATCH_TIME" ]; then
    echo -e "\n⏰ Last Batch Prepared: $LAST_BATCH_TIME"
fi