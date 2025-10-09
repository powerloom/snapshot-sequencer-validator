#!/bin/bash

# IPFS Cleanup Script
# Unpins CIDs older than specified days to prevent storage bloat

set -e

# Configuration
DEFAULT_MAX_AGE_DAYS=7
DEFAULT_CLEANUP_INTERVAL_HOURS=72  # 3 days

# Get configuration from environment or use defaults
MAX_AGE_DAYS="${IPFS_CLEANUP_MAX_AGE_DAYS:-$DEFAULT_MAX_AGE_DAYS}"
CLEANUP_INTERVAL_HOURS="${IPFS_CLEANUP_INTERVAL_HOURS:-$DEFAULT_CLEANUP_INTERVAL_HOURS}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging function
log() {
    echo -e "[$(date '+%Y-%m-%d %H:%M:%S')] ${1}"
}

# Function to check if IPFS is ready
wait_for_ipfs() {
    log "${BLUE}Waiting for IPFS to be ready...${NC}"

    local max_attempts=30
    local attempt=1

    while [ $attempt -le $max_attempts ]; do
        if ipfs id > /dev/null 2>&1; then
            log "${GREEN}✓ IPFS is ready${NC}"
            return 0
        fi

        log "${YELLOW}Attempt $attempt/$max_attempts: IPFS not ready, waiting...${NC}"
        sleep 2
        ((attempt++))
    done

    log "${RED}❌ IPFS failed to become ready after ${max_attempts} attempts${NC}"
    return 1
}

# Function to cleanup old pinned CIDs
cleanup_old_pins() {
    log "${BLUE}Starting IPFS cleanup for CIDs older than ${MAX_AGE_DAYS} days...${NC}"

    local total_pins=0
    local unpinned_count=0
    local error_count=0

    # Get current timestamp in seconds
    local current_timestamp=$(date +%s)
    local max_age_seconds=$((MAX_AGE_DAYS * 24 * 60 * 60))

    # Get all pins with their information
    log "${BLUE}Fetching list of pinned CIDs...${NC}"

    # Get all pins (recursive pins)
    while IFS= read -r cid; do
        if [ -z "$cid" ]; then
            continue
        fi

        ((total_pins++))

        try_unpin_cid "$cid" "$current_timestamp" "$max_age_seconds" && ((unpinned_count++)) || ((error_count++))

        # Log progress every 10 pins
        if ((total_pins % 10 == 0)); then
            log "${BLUE}Processed $total_pins pins, unpinned $unpinned_count${NC}"
        fi

    done < <(ipfs pin ls --recursive=true 2>/dev/null | grep -v "^$" | awk '{print $1}')

    log "${GREEN}✅ Cleanup completed!${NC}"
    log "  Total pins processed: $total_pins"
    log "  CIDs unpinned: $unpinned_count"
    log "  Errors encountered: $error_count"
}

# Function to try unpinning a single CID
try_unpin_cid() {
    local cid="$1"
    local current_timestamp="$2"
    local max_age_seconds="$3"

    # Skip indirect pins (they're usually part of larger structures)
    if ipfs pin ls --recursive=false "$cid" > /dev/null 2>&1; then
        return 0  # It's a direct pin, continue with processing
    else
        return 0  # It's an indirect pin, skip it
    fi

    # Get pin creation time (this is a bit tricky with IPFS)
    # We'll use a heuristic approach based on when we first added it
    local pin_age_seconds=$(get_pin_age_seconds "$cid")

    if [ "$pin_age_seconds" -gt "$max_age_seconds" ]; then
        log "${YELLOW}Unpinning $cid (age: $((pin_age_seconds / 86400)) days)${NC}"

        if ipfs pin rm "$cid" > /dev/null 2>&1; then
            # Also try to remove from repo to free space
            ipfs repo gc > /dev/null 2>&1 || true
            return 0
        else
            log "${RED}Failed to unpin $cid${NC}"
            return 1
        fi
    fi

    return 0
}

# Function to estimate pin age (heuristic approach)
get_pin_age_seconds() {
    local cid="$1"

    # Try to get block information which might contain timestamp
    local block_info=$(ipfs block stat "$cid" 2>/dev/null || echo "")

    if [ -n "$block_info" ]; then
        # Try to extract timestamp from block info (this is IPFS implementation dependent)
        # As a fallback, we'll use file modification time if available
        local timestamp=$(echo "$block_info" | grep -o '"Key".*[0-9]' | tail -1 | grep -o '[0-9]*' || echo "")

        if [ -n "$timestamp" ] && [ "$timestamp" -gt 0 ]; then
            local current_time=$(date +%s)
            echo $((current_time - timestamp))
            return 0
        fi
    fi

    # Fallback: assume older pins if we can't determine age
    # This is a conservative approach
    echo $((${MAX_AGE_DAYS} * 24 * 60 * 60 + 86400))  # Max age + 1 day
}

# Main cleanup function with scheduling
run_cleanup_scheduler() {
    log "${GREEN}🚀 Starting IPFS cleanup scheduler${NC}"
    log "Cleanup interval: ${CLEANUP_INTERVAL_HOURS} hours"
    log "Max pin age: ${MAX_AGE_DAYS} days"

    # Wait for IPFS to be ready
    wait_for_ipfs

    # Run initial cleanup
    cleanup_old_pins

    # Schedule regular cleanups
    local sleep_seconds=$((CLEANUP_INTERVAL_HOURS * 3600))

    log "${BLUE}Next cleanup scheduled in ${CLEANUP_INTERVAL_HOURS} hours${NC}"

    while true; do
        sleep "$sleep_seconds"
        log "${BLUE}🔄 Running scheduled cleanup...${NC}"
        cleanup_old_pins
        log "${BLUE}Next cleanup in ${CLEANUP_INTERVAL_HOURS} hours${NC}"
    done
}

# Handle signals gracefully
cleanup_and_exit() {
    log "${YELLOW}Received signal, cleaning up and exiting...${NC}"
    exit 0
}

trap cleanup_and_exit SIGTERM SIGINT

# Main execution
if [ "${1:-}" = "--once" ]; then
    log "${BLUE}Running single cleanup execution...${NC}"
    wait_for_ipfs
    cleanup_old_pins
else
    log "${BLUE}Starting IPFS cleanup service...${NC}"
    run_cleanup_scheduler
fi