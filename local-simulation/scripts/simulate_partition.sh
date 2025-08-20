#!/bin/bash

# simulate_partition.sh - Create network partitions between nodes
# Usage: ./simulate_partition.sh [PARTITION_TYPE] [NODES...]

set -e

PARTITION_TYPE=${1:-"split"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[PARTITION]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[PARTITION]${NC} $1"
}

error() {
    echo -e "${RED}[PARTITION]${NC} $1"
}

info() {
    echo -e "${BLUE}[PARTITION]${NC} $1"
}

# Function to get container IP
get_container_ip() {
    local container_name=$1
    docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$container_name" 2>/dev/null
}

# Function to block traffic between two containers
block_traffic_between() {
    local source_container=$1
    local target_container=$2
    
    local target_ip=$(get_container_ip "$target_container")
    if [ -z "$target_ip" ]; then
        error "Could not find IP for container: $target_container"
        return 1
    fi
    
    log "Blocking traffic from $source_container to $target_container ($target_ip)"
    
    # Block outgoing traffic to target
    docker exec -it "$source_container" iptables -A OUTPUT -d "$target_ip" -j DROP 2>/dev/null || {
        warn "Failed to block outgoing traffic from $source_container to $target_ip"
        return 1
    }
    
    # Also block using traffic control (backup method)
    docker exec -it "$source_container" sh -c "
        tc qdisc add dev eth0 root handle 1: htb 2>/dev/null || tc qdisc replace dev eth0 root handle 1: htb
        tc class add dev eth0 parent 1: classid 1:1 htb rate 1000mbit 2>/dev/null || true
        tc class add dev eth0 parent 1:1 classid 1:99 htb rate 1bit 2>/dev/null || true
        tc qdisc add dev eth0 parent 1:99 handle 99: netem loss 100% 2>/dev/null || true
        tc filter add dev eth0 parent 1: protocol ip prio 1 u32 match ip dst $target_ip flowid 1:99 2>/dev/null || true
    " 2>/dev/null || warn "TC-based blocking failed for $source_container -> $target_container"
}

# Function to restore traffic between two containers
restore_traffic_between() {
    local source_container=$1
    local target_container=$2
    
    local target_ip=$(get_container_ip "$target_container")
    if [ -z "$target_ip" ]; then
        error "Could not find IP for container: $target_container"
        return 1
    fi
    
    log "Restoring traffic from $source_container to $target_container ($target_ip)"
    
    # Remove iptables rule
    docker exec -it "$source_container" iptables -D OUTPUT -d "$target_ip" -j DROP 2>/dev/null || true
    
    # Reset traffic control
    docker exec -it "$source_container" tc qdisc del dev eth0 root 2>/dev/null || true
}

# Function to create a network split (two partitions)
create_network_split() {
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    # Default split: bootstrap + sequencer-1 vs sequencer-2 + sequencer-3, debugger isolated
    local partition1=("p2p-bootstrap" "p2p-sequencer-1")
    local partition2=("p2p-sequencer-2" "p2p-sequencer-3")
    local isolated=("p2p-debugger")
    
    log "Creating network partition:"
    info "Partition 1: ${partition1[*]}"
    info "Partition 2: ${partition2[*]}"
    info "Isolated: ${isolated[*]}"
    
    # Block traffic between partitions
    for node1 in "${partition1[@]}"; do
        for node2 in "${partition2[@]}"; do
            block_traffic_between "$node1" "$node2"
            block_traffic_between "$node2" "$node1"
        done
        
        # Isolate debugger from partition 1
        for isolated_node in "${isolated[@]}"; do
            block_traffic_between "$node1" "$isolated_node"
            block_traffic_between "$isolated_node" "$node1"
        done
    done
    
    # Isolate debugger from partition 2
    for node2 in "${partition2[@]}"; do
        for isolated_node in "${isolated[@]}"; do
            block_traffic_between "$node2" "$isolated_node"
            block_traffic_between "$isolated_node" "$node2"
        done
    done
    
    log "Network partition created successfully"
    info "Partition 1 can only communicate internally"
    info "Partition 2 can only communicate internally"
    info "Isolated nodes have no connectivity"
}

# Function to isolate specific nodes
isolate_nodes() {
    local nodes_to_isolate=("$@")
    local all_containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    log "Isolating nodes: ${nodes_to_isolate[*]}"
    
    for isolated_node in "${nodes_to_isolate[@]}"; do
        info "Isolating $isolated_node from all other nodes"
        for other_node in "${all_containers[@]}"; do
            if [ "$isolated_node" != "$other_node" ]; then
                block_traffic_between "$isolated_node" "$other_node"
                block_traffic_between "$other_node" "$isolated_node"
            fi
        done
    done
    
    log "Node isolation complete"
}

# Function to create bootstrap isolation (common failure scenario)
isolate_bootstrap() {
    log "Isolating bootstrap node (simulating bootstrap failure)"
    
    local other_nodes=("p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    for node in "${other_nodes[@]}"; do
        block_traffic_between "p2p-bootstrap" "$node"
        block_traffic_between "$node" "p2p-bootstrap"
    done
    
    log "Bootstrap node isolated - testing peer discovery resilience"
}

# Function to create custom partition from command line arguments
create_custom_partition() {
    local partition_spec="$*"
    log "Creating custom partition: $partition_spec"
    
    # Parse partition specification: "node1,node2|node3,node4"
    IFS='|' read -ra PARTITIONS <<< "$partition_spec"
    
    local partition_nodes=()
    local partition_count=0
    
    for partition in "${PARTITIONS[@]}"; do
        IFS=',' read -ra NODES <<< "$partition"
        partition_nodes[$partition_count]="${NODES[@]}"
        info "Partition $((partition_count + 1)): ${NODES[*]}"
        ((partition_count++))
    done
    
    # Block traffic between different partitions
    for ((i=0; i<partition_count; i++)); do
        for ((j=i+1; j<partition_count; j++)); do
            IFS=' ' read -ra PARTITION_I <<< "${partition_nodes[$i]}"
            IFS=' ' read -ra PARTITION_J <<< "${partition_nodes[$j]}"
            
            for node_i in "${PARTITION_I[@]}"; do
                for node_j in "${PARTITION_J[@]}"; do
                    block_traffic_between "$node_i" "$node_j"
                    block_traffic_between "$node_j" "$node_i"
                done
            done
        done
    done
    
    log "Custom partition created successfully"
}

# Function to simulate node failures
simulate_node_failure() {
    local failed_node=$1
    
    log "Simulating complete failure of node: $failed_node"
    
    # Stop the container to simulate complete failure
    docker-compose -f ../docker/docker-compose.yml stop "$failed_node" 2>/dev/null || {
        error "Failed to stop container: $failed_node"
        return 1
    }
    
    log "$failed_node has been stopped (simulating node failure)"
    info "Use 'docker-compose -f ../docker/docker-compose.yml start $failed_node' to recover"
}

# Function to check partition status
check_partition_status() {
    log "Checking current partition status..."
    
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    for container in "${containers[@]}"; do
        info "=== $container ==="
        
        # Check iptables rules
        local iptables_rules=$(docker exec "$container" iptables -L OUTPUT 2>/dev/null | grep DROP || echo "No DROP rules")
        echo "  IPTables: $iptables_rules"
        
        # Check traffic control
        local tc_rules=$(docker exec "$container" tc qdisc show dev eth0 2>/dev/null | grep -v "noqueue" || echo "No TC rules")
        echo "  TC: $tc_rules"
        
        # Check container status
        local status=$(docker inspect "$container" --format='{{.State.Status}}' 2>/dev/null || echo "not found")
        echo "  Status: $status"
        
        echo
    done
}

# Main execution
main() {
    log "Starting network partition simulation"
    log "Partition type: $PARTITION_TYPE"
    
    # Check if docker compose is running
    if ! docker-compose -f ../docker/docker-compose.yml ps | grep -q "Up"; then
        error "Docker compose services are not running. Start them first with:"
        error "cd ../docker && docker-compose up -d"
        exit 1
    fi
    
    case "$PARTITION_TYPE" in
        "split")
            create_network_split
            ;;
        "isolate")
            shift  # Remove first argument (partition type)
            if [ $# -eq 0 ]; then
                error "Isolate mode requires at least one node name"
                exit 1
            fi
            isolate_nodes "$@"
            ;;
        "bootstrap")
            isolate_bootstrap
            ;;
        "custom")
            shift  # Remove first argument
            if [ $# -eq 0 ]; then
                error "Custom mode requires partition specification: 'node1,node2|node3,node4'"
                exit 1
            fi
            create_custom_partition "$@"
            ;;
        "fail")
            if [ $# -lt 2 ]; then
                error "Fail mode requires node name"
                exit 1
            fi
            simulate_node_failure "$2"
            ;;
        "status")
            check_partition_status
            ;;
        *)
            error "Unknown partition type: $PARTITION_TYPE"
            exit 1
            ;;
    esac
    
    if [ "$PARTITION_TYPE" != "status" ]; then
        log "Network partition simulation complete!"
        info "Use './reset_network.sh' to restore full connectivity"
        info "Use '$0 status' to check current partition state"
    fi
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Network Partition Simulator

Usage: $0 [PARTITION_TYPE] [PARAMETERS...]

Partition Types:
  split                     - Split network into two partitions (default)
  isolate NODE1 [NODE2...]  - Isolate specific nodes from all others
  bootstrap                 - Isolate bootstrap node (test discovery resilience)
  custom "part1|part2|..."  - Create custom partitions
  fail NODE                 - Simulate complete node failure (stops container)
  status                    - Show current partition status

Available containers:
  - p2p-bootstrap
  - p2p-sequencer-1
  - p2p-sequencer-2
  - p2p-sequencer-3
  - p2p-debugger

Examples:
  $0 split                                        # Default network split
  $0 isolate p2p-sequencer-1                     # Isolate sequencer-1
  $0 bootstrap                                    # Isolate bootstrap node
  $0 custom "p2p-bootstrap,p2p-sequencer-1|p2p-sequencer-2,p2p-sequencer-3"
  $0 fail p2p-sequencer-2                        # Stop sequencer-2 container
  $0 status                                       # Check current state

Recovery:
  ./reset_network.sh                              # Remove all network conditions
  docker-compose -f ../docker/docker-compose.yml start NODE  # Restart failed node

EOF
    exit 0
fi

# Run main function
main "$@"