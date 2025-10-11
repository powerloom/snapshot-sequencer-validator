#!/bin/bash

# Monitor API Client - Simple curl-based monitoring
# Usage: ./monitor_api_client.sh [port] [protocol] [market]
# No jq dependency, uses grep/sed/awk for parsing
#
# SECURITY NOTE:
# The monitor-api service is bound to localhost (127.0.0.1) for security.
# To access from remote machines, use SSH tunneling:
#   ssh -L 9091:localhost:9091 user@vps-ip
# Then access via http://localhost:9091 on your local machine

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Arguments: port, protocol, market (all optional)
MONITOR_API_PORT=${1:-${MONITOR_API_PORT:-8080}}
PROTOCOL=${2:-${PROTOCOL:-}}
MARKET=${3:-${MARKET:-}}

API_BASE="http://localhost:${MONITOR_API_PORT}/api/v1"

if [ -n "$PROTOCOL" ]; then
    echo -e "${YELLOW}Using PROTOCOL: ${PROTOCOL}${NC}"
fi
if [ -n "$MARKET" ]; then
    echo -e "${YELLOW}Using MARKET: ${MARKET}${NC}"
fi

print_header() {
    echo -e "${CYAN}═══════════════════════════════════════════════${NC}"
    echo -e "${CYAN}$1${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════${NC}"
}

print_section() {
    echo -e "\n${BLUE}$1${NC}"
}

extract_json_value() {
    local json="$1"
    local key="$2"
    echo "$json" | grep -o "\"$key\":[^,}]*" | sed "s/\"$key\"://" | sed 's/^"\(.*\)"$/\1/' | sed 's/^[[:space:]]*//'
}

# Check API health
check_health() {
    response=$(curl -s "${API_BASE}/health" 2>/dev/null || echo "")
    if [ -z "$response" ]; then
        echo -e "${RED}❌ Monitor API not reachable on port ${MONITOR_API_PORT}${NC}"
        echo -e "${YELLOW}Make sure monitor-api service is running${NC}"
        exit 1
    fi

    status=$(extract_json_value "$response" "status")
    if [ "$status" = "healthy" ]; then
        echo -e "${GREEN}✅ Monitor API is healthy${NC}"
    else
        echo -e "${RED}❌ Monitor API unhealthy${NC}"
        exit 1
    fi
}

# Pipeline overview
show_overview() {
    print_header "📊 PIPELINE OVERVIEW"

    response=$(curl -s "${API_BASE}/pipeline/overview")

    # Submission queue
    sq_depth=$(extract_json_value "$response" "depth" | head -1)
    sq_status=$(extract_json_value "$response" "status" | head -1)

    # Counts
    active_windows=$(echo "$response" | grep -o '"active_windows":[0-9]*' | cut -d: -f2)
    ready_batches=$(echo "$response" | grep -o '"ready_batches":[0-9]*' | cut -d: -f2)
    active_workers=$(echo "$response" | grep -o '"active_workers":[0-9]*' | cut -d: -f2)
    completed_parts=$(echo "$response" | grep -o '"completed_parts":[0-9]*' | cut -d: -f2)
    finalized_batches=$(echo "$response" | grep -o '"finalized_batches":[0-9]*' | cut -d: -f2)
    aggregated_batches=$(echo "$response" | grep -o '"aggregated_batches":[0-9]*' | cut -d: -f2)

    echo -e "${CYAN}Submission Queue:${NC}       ${sq_depth} (${sq_status})"
    echo -e "${CYAN}Active Windows:${NC}         ${active_windows}"
    echo -e "${CYAN}Ready Batches:${NC}          ${ready_batches}"
    echo -e "${CYAN}Active Workers:${NC}         ${active_workers}"
    echo -e "${CYAN}Completed Parts:${NC}        ${completed_parts}"
    echo -e "${CYAN}Finalized Batches:${NC}      ${finalized_batches}"
    echo -e "${CYAN}Aggregated Batches:${NC}     ${aggregated_batches}"
}

# Submission windows
show_windows() {
    print_section "🪟 Active Submission Windows"

    response=$(curl -s "${API_BASE}/submissions/windows")
    count=$(echo "$response" | grep -o '"epoch_id"' | wc -l | tr -d ' ')

    if [ "$count" -eq 0 ]; then
        echo "  No active windows"
        return
    fi

    echo "  Found $count active window(s)"

    # Extract epoch IDs
    echo "$response" | grep -o '"epoch_id":[0-9]*' | cut -d: -f2 | while read epoch; do
        echo "  • Epoch: $epoch"
    done
}

# Worker status
show_workers() {
    print_section "👷 Worker Status"

    response=$(curl -s "${API_BASE}/workers/status")
    count=$(echo "$response" | grep -o '"worker_id"' | wc -l | tr -d ' ')

    if [ "$count" -eq 0 ]; then
        echo "  No active workers"
        return
    fi

    echo "  Found $count active worker(s)"
}

# Finalized batches
show_finalized() {
    print_section "📦 Recent Finalized Batches"

    response=$(curl -s "${API_BASE}/batches/finalized")
    count=$(echo "$response" | grep -o '"epoch_id"' | wc -l | tr -d ' ')

    if [ "$count" -eq 0 ]; then
        echo "  No finalized batches"
        return
    fi

    echo "  Found $count finalized batch(es)"

    # Extract epoch IDs and show first 5
    echo "$response" | grep -o '"epoch_id":[0-9]*' | cut -d: -f2 | head -5 | while read epoch; do
        # Try to extract project count for this epoch
        projects=$(echo "$response" | grep -A 20 "\"epoch_id\":$epoch" | grep -o '"project_count":[0-9]*' | head -1 | cut -d: -f2)
        if [ -n "$projects" ]; then
            echo "  • Epoch $epoch: $projects projects"
        else
            echo "  • Epoch $epoch"
        fi
    done
}

# Aggregated batches
show_aggregated() {
    print_section "🌐 Recent Aggregated Batches"

    response=$(curl -s "${API_BASE}/aggregation/results")
    count=$(echo "$response" | grep -o '"epoch_id"' | wc -l | tr -d ' ')

    if [ "$count" -eq 0 ]; then
        echo "  No aggregated batches"
        return
    fi

    echo "  Found $count aggregated batch(es)"

    # Extract epoch IDs and show first 5
    echo "$response" | grep -o '"epoch_id":[0-9]*' | cut -d: -f2 | head -5 | while read epoch; do
        validators=$(echo "$response" | grep -A 20 "\"epoch_id\":$epoch" | grep -o '"validator_count":[0-9]*' | head -1 | cut -d: -f2)
        if [ -n "$validators" ]; then
            echo "  • Epoch $epoch: $validators validators"
        else
            echo "  • Epoch $epoch"
        fi
    done
}

# Main execution
main() {
    check_health
    echo ""
    show_overview
    show_windows
    show_workers
    show_finalized
    show_aggregated

    echo ""
    echo -e "${YELLOW}📊 Full API documentation: http://localhost:${MONITOR_API_PORT}/swagger/index.html${NC}"
    echo -e "${YELLOW}🔐 Remote access: ssh -L ${MONITOR_API_PORT}:localhost:${MONITOR_API_PORT} user@vps-ip${NC}"
}

main