#!/bin/bash

# simulate_packet_loss.sh - Simulate packet loss between nodes
# Usage: ./simulate_packet_loss.sh [LOSS_PERCENT] [TARGET_NODE] [SOURCE_NODE]

set -e

LOSS_PERCENT=${1:-5}
TARGET_NODE=${2:-"all"}
SOURCE_NODE=${3:-"all"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[PACKET-LOSS]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[PACKET-LOSS]${NC} $1"
}

error() {
    echo -e "${RED}[PACKET-LOSS]${NC} $1"
}

# Function to add packet loss to a container
add_packet_loss_to_container() {
    local container_name=$1
    local loss_percent=$2
    
    log "Adding ${loss_percent}% packet loss to container: $container_name"
    
    # Try to add new qdisc or replace existing one
    docker exec -it "$container_name" tc qdisc add dev eth0 root netem loss "${loss_percent}%" 2>/dev/null || {
        docker exec -it "$container_name" tc qdisc replace dev eth0 root netem loss "${loss_percent}%" 2>/dev/null || {
            warn "Failed to add packet loss to $container_name - traffic control may not be available"
            return 1
        }
    }
    
    log "Successfully added ${loss_percent}% packet loss to $container_name"
}

# Function to add packet loss with existing latency
add_packet_loss_with_latency() {
    local container_name=$1
    local loss_percent=$2
    
    log "Adding ${loss_percent}% packet loss to container with existing conditions: $container_name"
    
    # Check if there are existing netem rules
    local existing_rules=$(docker exec "$container_name" tc qdisc show dev eth0 2>/dev/null | grep netem | head -1)
    
    if [ ! -z "$existing_rules" ]; then
        # Extract existing delay if present
        local existing_delay=$(echo "$existing_rules" | grep -oP 'delay \K[0-9.]+ms' || echo "")
        
        if [ ! -z "$existing_delay" ]; then
            log "Preserving existing delay: $existing_delay"
            docker exec -it "$container_name" tc qdisc replace dev eth0 root netem delay "$existing_delay" loss "${loss_percent}%" 2>/dev/null || {
                warn "Failed to combine packet loss with existing delay, applying standalone packet loss"
                add_packet_loss_to_container "$container_name" "$loss_percent"
            }
        else
            add_packet_loss_to_container "$container_name" "$loss_percent"
        fi
    else
        add_packet_loss_to_container "$container_name" "$loss_percent"
    fi
}

# Function to add packet loss between specific nodes
add_packet_loss_between_nodes() {
    local source=$1
    local target=$2
    local loss_percent=$3
    
    log "Adding ${loss_percent}% packet loss from $source to $target"
    
    # Get target container IP
    local target_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$target" 2>/dev/null)
    
    if [ -z "$target_ip" ]; then
        error "Could not find IP for container: $target"
        return 1
    fi
    
    # Add targeted packet loss using iptables and tc
    docker exec -it "$source" sh -c "
        # Set up HTB if not exists
        tc qdisc add dev eth0 root handle 1: htb 2>/dev/null || tc qdisc replace dev eth0 root handle 1: htb
        tc class add dev eth0 parent 1: classid 1:1 htb rate 1000mbit 2>/dev/null || true
        tc class add dev eth0 parent 1:1 classid 1:10 htb rate 1000mbit 2>/dev/null || true
        
        # Add packet loss for this specific target
        tc qdisc add dev eth0 parent 1:10 handle 10: netem loss ${loss_percent}% 2>/dev/null || \
        tc qdisc replace dev eth0 parent 1:10 handle 10: netem loss ${loss_percent}%
        
        # Filter traffic to target IP
        tc filter add dev eth0 parent 1: protocol ip prio 1 u32 match ip dst $target_ip flowid 1:10 2>/dev/null || true
    " 2>/dev/null || {
        warn "Targeted packet loss setup failed for $source -> $target, falling back to general packet loss"
        add_packet_loss_with_latency "$source" "$loss_percent"
    }
}

# Function to simulate burst packet loss
simulate_burst_loss() {
    local container_name=$1
    local loss_percent=$2
    local burst_duration=${3:-5}
    
    log "Simulating burst packet loss: ${loss_percent}% for ${burst_duration} seconds on $container_name"
    
    # Add high packet loss
    docker exec -it "$container_name" tc qdisc replace dev eth0 root netem loss "${loss_percent}%" 2>/dev/null || {
        warn "Failed to apply burst packet loss to $container_name"
        return 1
    }
    
    log "Burst packet loss active for ${burst_duration} seconds..."
    sleep "$burst_duration"
    
    # Reduce to normal levels
    local normal_loss=$((loss_percent / 4))  # Reduce to 25% of burst loss
    docker exec -it "$container_name" tc qdisc replace dev eth0 root netem loss "${normal_loss}%" 2>/dev/null || true
    
    log "Burst complete, reduced to ${normal_loss}% packet loss"
}

# Main execution
main() {
    log "Starting packet loss simulation"
    log "Packet Loss: ${LOSS_PERCENT}%, Target: $TARGET_NODE, Source: $SOURCE_NODE"
    
    # Validate loss percentage
    if ! [[ "$LOSS_PERCENT" =~ ^[0-9]+(\.[0-9]+)?$ ]] || (( $(echo "$LOSS_PERCENT > 100" | bc -l) )); then
        error "Invalid loss percentage: $LOSS_PERCENT (must be 0-100)"
        exit 1
    fi
    
    # Define available containers
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    # Check if docker compose is running
    if ! docker-compose -f ../docker/docker-compose.yml ps | grep -q "Up"; then
        error "Docker compose services are not running. Start them first with:"
        error "cd ../docker && docker-compose up -d"
        exit 1
    fi
    
    # Apply packet loss based on parameters
    if [ "$SOURCE_NODE" = "all" ] && [ "$TARGET_NODE" = "all" ]; then
        # Add packet loss to all containers
        log "Adding ${LOSS_PERCENT}% packet loss to all nodes"
        for container in "${containers[@]}"; do
            add_packet_loss_with_latency "$container" "$LOSS_PERCENT" || true
        done
        
    elif [ "$SOURCE_NODE" = "all" ] && [ "$TARGET_NODE" != "all" ]; then
        # Add packet loss from all nodes to specific target
        log "Adding ${LOSS_PERCENT}% packet loss from all nodes to $TARGET_NODE"
        for container in "${containers[@]}"; do
            if [ "$container" != "$TARGET_NODE" ]; then
                add_packet_loss_between_nodes "$container" "$TARGET_NODE" "$LOSS_PERCENT" || true
            fi
        done
        
    elif [ "$SOURCE_NODE" != "all" ] && [ "$TARGET_NODE" = "all" ]; then
        # Add packet loss from specific source to all nodes
        log "Adding ${LOSS_PERCENT}% packet loss from $SOURCE_NODE to all nodes"
        add_packet_loss_with_latency "$SOURCE_NODE" "$LOSS_PERCENT"
        
    else
        # Add packet loss between specific nodes
        log "Adding ${LOSS_PERCENT}% packet loss from $SOURCE_NODE to $TARGET_NODE"
        add_packet_loss_between_nodes "$SOURCE_NODE" "$TARGET_NODE" "$LOSS_PERCENT"
    fi
    
    # Show current network conditions
    log "Current network conditions:"
    for container in "${containers[@]}"; do
        if docker exec "$container" tc qdisc show dev eth0 2>/dev/null | grep -q netem; then
            local conditions=$(docker exec "$container" tc qdisc show dev eth0 2>/dev/null | grep netem | head -1)
            log "$container: $conditions"
        else
            log "$container: no netem rules found"
        fi
    done
    
    log "Packet loss simulation applied successfully!"
    log "Use ./reset_network.sh to remove all network conditions"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Packet Loss Simulator

Usage: $0 [LOSS_PERCENT] [TARGET_NODE] [SOURCE_NODE]

Parameters:
  LOSS_PERCENT - Percentage of packets to drop (0-100, default: 5)
  TARGET_NODE  - Target container name or 'all' (default: all)
  SOURCE_NODE  - Source container name or 'all' (default: all)

Available containers:
  - p2p-bootstrap
  - p2p-sequencer-1
  - p2p-sequencer-2
  - p2p-sequencer-3
  - p2p-debugger

Special modes:
  --burst LOSS CONTAINER [DURATION] - Simulate burst packet loss

Examples:
  $0 10                                     # Add 10% loss to all nodes
  $0 5 p2p-sequencer-1                     # Add 5% loss from all nodes to sequencer-1
  $0 15 p2p-sequencer-2 p2p-sequencer-1   # Add 15% loss from sequencer-1 to sequencer-2
  $0 --burst 50 p2p-bootstrap 10           # 50% burst loss for 10 seconds

EOF
    exit 0
fi

# Handle burst mode
if [ "$1" = "--burst" ]; then
    if [ $# -lt 3 ]; then
        error "Burst mode requires: --burst LOSS_PERCENT CONTAINER [DURATION]"
        exit 1
    fi
    simulate_burst_loss "$3" "$2" "${4:-5}"
    exit 0
fi

# Run main function
main "$@"