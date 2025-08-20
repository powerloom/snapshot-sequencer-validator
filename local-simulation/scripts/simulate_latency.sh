#!/bin/bash

# simulate_latency.sh - Add network latency between nodes
# Usage: ./simulate_latency.sh [LATENCY_MS] [TARGET_NODE] [SOURCE_NODE]

set -e

LATENCY_MS=${1:-100}
TARGET_NODE=${2:-"all"}
SOURCE_NODE=${3:-"all"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[LATENCY-SIM]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[LATENCY-SIM]${NC} $1"
}

error() {
    echo -e "${RED}[LATENCY-SIM]${NC} $1"
}

# Function to add latency to a specific container
add_latency_to_container() {
    local container_name=$1
    local latency_ms=$2
    
    log "Adding ${latency_ms}ms latency to container: $container_name"
    
    docker exec -it "$container_name" tc qdisc add dev eth0 root netem delay "${latency_ms}ms" 2>/dev/null || {
        # If tc is not available or qdisc exists, try to replace
        docker exec -it "$container_name" tc qdisc replace dev eth0 root netem delay "${latency_ms}ms" 2>/dev/null || {
            warn "Failed to add latency to $container_name - traffic control may not be available"
            return 1
        }
    }
    
    log "Successfully added ${latency_ms}ms latency to $container_name"
}

# Function to add latency between specific nodes
add_latency_between_nodes() {
    local source=$1
    local target=$2
    local latency_ms=$3
    
    log "Adding ${latency_ms}ms latency from $source to $target"
    
    # Get target container IP
    local target_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$target" 2>/dev/null)
    
    if [ -z "$target_ip" ]; then
        error "Could not find IP for container: $target"
        return 1
    fi
    
    # Add targeted latency using iptables and tc
    docker exec -it "$source" sh -c "
        # Create a class for the target IP
        tc qdisc add dev eth0 root handle 1: htb 2>/dev/null || tc qdisc replace dev eth0 root handle 1: htb
        tc class add dev eth0 parent 1: classid 1:1 htb rate 1000mbit
        tc class add dev eth0 parent 1:1 classid 1:10 htb rate 1000mbit
        
        # Add delay for this specific target
        tc qdisc add dev eth0 parent 1:10 handle 10: netem delay ${latency_ms}ms
        
        # Filter traffic to target IP
        tc filter add dev eth0 parent 1: protocol ip prio 1 u32 match ip dst $target_ip flowid 1:10
    " 2>/dev/null || {
        warn "Targeted latency setup failed for $source -> $target, falling back to general latency"
        add_latency_to_container "$source" "$latency_ms"
    }
}

# Main execution
main() {
    log "Starting network latency simulation"
    log "Latency: ${LATENCY_MS}ms, Target: $TARGET_NODE, Source: $SOURCE_NODE"
    
    # Define available containers
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    # Check if docker compose is running
    if ! docker-compose -f ../docker/docker-compose.yml ps | grep -q "Up"; then
        error "Docker compose services are not running. Start them first with:"
        error "cd ../docker && docker-compose up -d"
        exit 1
    fi
    
    # Apply latency based on parameters
    if [ "$SOURCE_NODE" = "all" ] && [ "$TARGET_NODE" = "all" ]; then
        # Add latency to all containers
        log "Adding ${LATENCY_MS}ms latency to all nodes"
        for container in "${containers[@]}"; do
            add_latency_to_container "$container" "$LATENCY_MS" || true
        done
        
    elif [ "$SOURCE_NODE" = "all" ] && [ "$TARGET_NODE" != "all" ]; then
        # Add latency from all nodes to specific target
        log "Adding ${LATENCY_MS}ms latency from all nodes to $TARGET_NODE"
        for container in "${containers[@]}"; do
            if [ "$container" != "$TARGET_NODE" ]; then
                add_latency_between_nodes "$container" "$TARGET_NODE" "$LATENCY_MS" || true
            fi
        done
        
    elif [ "$SOURCE_NODE" != "all" ] && [ "$TARGET_NODE" = "all" ]; then
        # Add latency from specific source to all nodes
        log "Adding ${LATENCY_MS}ms latency from $SOURCE_NODE to all nodes"
        add_latency_to_container "$SOURCE_NODE" "$LATENCY_MS"
        
    else
        # Add latency between specific nodes
        log "Adding ${LATENCY_MS}ms latency from $SOURCE_NODE to $TARGET_NODE"
        add_latency_between_nodes "$SOURCE_NODE" "$TARGET_NODE" "$LATENCY_MS"
    fi
    
    # Log current network conditions
    log "Current network conditions:"
    for container in "${containers[@]}"; do
        log "Checking $container..."
        docker exec "$container" tc qdisc show dev eth0 2>/dev/null | head -1 || warn "$container: no tc rules found"
    done
    
    log "Latency simulation applied successfully!"
    log "Use ./reset_network.sh to remove all network conditions"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Network Latency Simulator

Usage: $0 [LATENCY_MS] [TARGET_NODE] [SOURCE_NODE]

Parameters:
  LATENCY_MS   - Latency to add in milliseconds (default: 100)
  TARGET_NODE  - Target container name or 'all' (default: all)
  SOURCE_NODE  - Source container name or 'all' (default: all)

Available containers:
  - p2p-bootstrap
  - p2p-sequencer-1
  - p2p-sequencer-2
  - p2p-sequencer-3
  - p2p-debugger

Examples:
  $0 200                                    # Add 200ms to all nodes
  $0 50 p2p-sequencer-1                    # Add 50ms from all nodes to sequencer-1
  $0 75 p2p-sequencer-2 p2p-sequencer-1   # Add 75ms from sequencer-1 to sequencer-2
  $0 300 all p2p-bootstrap                 # Add 300ms from bootstrap to all nodes

EOF
    exit 0
fi

# Run main function
main "$@"