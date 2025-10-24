#!/bin/bash

# Cleanup script for timeline entries with scientific notation
# This removes old timeline entries that still have scientific notation

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}=== Timeline Scientific Notation Cleanup Script ===${NC}"
echo

# Protocol and market from the existing queue key
PROTOCOL="0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401"
MARKET="0xae32c4FA72E2e5F53ed4D214E4aD049286Ded16f"
TIMELINE_KEY="${PROTOCOL}:${MARKET}:metrics:batches:timeline"

echo "Checking timeline: $TIMELINE_KEY"

# Check Redis connection
echo "Checking Redis connection..."
if ! redis-cli -p 6380 ping >/dev/null 2>&1; then
    if ! docker exec snapshot-sequencer-validator-redis-1 redis-cli ping >/dev/null 2>&1; then
        echo -e "${RED}Error: Cannot connect to Redis${NC}"
        exit 1
    else
        REDIS_CMD="docker exec snapshot-sequencer-validator-redis-1 redis-cli"
        echo "Using Redis via Docker container"
    fi
else
    REDIS_CMD="redis-cli -p 6380"
fi

# Count total timeline entries
TOTAL_ENTRIES=$($REDIS_CMD ZCARD "$TIMELINE_KEY" | tr -d '\r\n')
echo "Total timeline entries: $TOTAL_ENTRIES"

# Count entries with scientific notation
echo "Counting entries with scientific notation..."
SCIENTIFIC_NOTATION_COUNT=0

# Get all entries and check for scientific notation
ENTRIES=$($REDIS_CMD ZREVRANGE "$TIMELINE_KEY" 0 -1)
for entry in $ENTRIES; do
    if [[ "$entry" == *"e+"* ]] || [[ "$entry" == *"E+"* ]]; then
        ((SCIENTIFIC_NOTATION_COUNT++))
    fi
done

echo "Entries with scientific notation: $SCIENTIFIC_NOTATION_COUNT"

if [ "$SCIENTIFIC_NOTATION_COUNT" -eq 0 ]; then
    echo -e "${GREEN}✓ No scientific notation entries found. Timeline is clean.${NC}"
    exit 0
fi

echo -e "${YELLOW}WARNING: Found $SCIENTIFIC_NOTATION_COUNT entries with scientific notation${NC}"
echo "These will be removed from the timeline."
echo
echo "Note: New entries will be created with proper integer formatting."
echo

# Ask for confirmation
read -p "Are you sure you want to continue? (type 'yes' to confirm): " confirm
if [ "$confirm" != "yes" ]; then
    echo "Cleanup cancelled."
    exit 0
fi

echo
echo "Starting cleanup..."

# Get all entries to keep (without scientific notation)
echo "Identifying entries to keep..."
TO_KEEP=""
COUNT=0

for entry in $ENTRIES; do
    if [[ "$entry" != *"e+"* ]] && [[ "$entry" != *"E+"* ]]; then
        if [ -z "$TO_KEEP" ]; then
            TO_KEEP="$entry"
        else
            TO_KEEP="$TO_KEEP $entry"
        fi
        ((COUNT++))
    fi
done

echo "Entries to keep: $COUNT"

if [ "$COUNT" -gt 0 ]; then
    echo "Recreating timeline with clean entries..."

    # Delete old timeline
    $REDIS_CMD DEL "$TIMELINE_KEY" > /dev/null

    # Add back clean entries
    for entry in $TO_KEEP; do
        $REDIS_CMD ZADD "$TIMELINE_KEY" 0 "$entry" > /dev/null
    done
else
    echo "No clean entries to keep, deleting timeline..."
    $REDIS_CMD DEL "$TIMELINE_KEY" > /dev/null
fi

# Verify cleanup
NEW_COUNT=$($REDIS_CMD ZCARD "$TIMELINE_KEY" | tr -d '\r\n')
echo "New timeline entry count: $NEW_COUNT"

echo -e "${GREEN}✓ Timeline cleanup completed successfully!${NC}"
echo -e "${GREEN}✓ Removed $SCIENTIFIC_NOTATION_COUNT scientific notation entries${NC}"
echo
echo -e "${YELLOW}Note: New timeline entries will be created with proper integer format${NC}"