    #!/bin/bash

# Cleanup script for stale aggregation queue items
# This script removes the unused aggregation queue items that accumulate over time

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}=== Aggregation Queue Cleanup Script ===${NC}"
echo

# Check if Redis is accessible (try both direct and container)
echo "Checking Redis connection..."
if ! redis-cli -p 6380 ping >/dev/null 2>&1; then
    if ! docker exec snapshot-sequencer-validator-redis-1 redis-cli ping >/dev/null 2>&1; then
        echo -e "${RED}Error: Cannot connect to Redis${NC}"
        echo "Please ensure Redis is running and accessible"
        exit 1
    else
        REDIS_CMD="docker exec snapshot-sequencer-validator-redis-1 redis-cli"
        echo "Using Redis via Docker container"
    fi
else
    REDIS_CMD="redis-cli -p 6380"
fi

# Get current queue depth
QUEUE_KEY="0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401:0xae32c4FA72E2e5F53ed4D214E4aD049286Ded16f:aggregation:queue"
CURRENT_DEPTH=$($REDIS_CMD LLEN "$QUEUE_KEY" | tr -d '\r\n')

echo "Current aggregation queue depth: $CURRENT_DEPTH"

if [ "$CURRENT_DEPTH" -eq 0 ]; then
    echo -e "${GREEN}Queue is already empty. Nothing to clean up.${NC}"
    exit 0
fi

# Show warning
echo -e "${YELLOW}WARNING: This will remove all $CURRENT_DEPTH items from the aggregation queue${NC}"
echo "Queue key: $QUEUE_KEY"
echo
echo "Note: This queue is NOT used by the active aggregation system."
echo "The active system uses Redis streams, not this list."
echo

# Ask for confirmation
read -p "Are you sure you want to continue? (type 'yes' to confirm): " confirm
if [ "$confirm" != "yes" ]; then
    echo "Cleanup cancelled."
    exit 0
fi

echo
echo "Starting cleanup..."

# Measure time
start_time=$(date +%s)

# Delete the queue
if $REDIS_CMD DEL "$QUEUE_KEY" > /dev/null; then
    end_time=$(date +%s)
    duration=$((end_time - start_time))

    echo -e "${GREEN}✓ Cleanup completed successfully in ${duration}s${NC}"

    # Verify cleanup
    new_depth=$($REDIS_CMD LLEN "$QUEUE_KEY" | tr -d '\r\n')
    echo "New queue depth: $new_depth"

    if [ "$new_depth" -eq 0 ]; then
        echo -e "${GREEN}✓ Verification successful: Queue is now empty${NC}"
    else
        echo -e "${YELLOW}⚠ Warning: Queue still has $new_depth items${NC}"
    fi
else
    echo -e "${RED}✗ Cleanup failed${NC}"
    exit 1
fi

echo
echo -e "${GREEN}=== Cleanup Complete ===${NC}"