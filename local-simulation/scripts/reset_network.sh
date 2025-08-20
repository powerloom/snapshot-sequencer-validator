#!/bin/bash

# reset_network.sh - Reset all network simulation conditions
# Usage: ./reset_network.sh [--restart]

set -e

RESTART_SERVICES=${1:-"false"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[RESET]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[RESET]${NC} $1"
}

error() {
    echo -e "${RED}[RESET]${NC} $1"
}

info() {
    echo -e "${BLUE}[RESET]${NC} $1"
}

# Function to reset network conditions on a container
reset_container_network() {
    local container_name=$1
    
    log "Resetting network conditions for: $container_name"
    
    # Check if container is running
    if ! docker exec "$container_name" echo "Container is running" >/dev/null 2>&1; then
        warn "$container_name is not running, skipping network reset"
        return 0
    fi
    
    # Reset Traffic Control (tc) rules
    docker exec -it "$container_name" sh -c '
        # Remove all qdisc rules
        tc qdisc del dev eth0 root 2>/dev/null || true
        
        # Flush all tc classes and filters
        tc class del dev eth0 classid 1: 2>/dev/null || true
        tc filter del dev eth0 2>/dev/null || true
        
        # Reset to default qdisc
        tc qdisc add dev eth0 root pfifo_fast 2>/dev/null || true
    ' 2>/dev/null || warn "Failed to reset TC rules for $container_name"
    
    # Reset IPTables rules
    docker exec -it "$container_name" sh -c '
        # Flush OUTPUT chain (where we add DROP rules)
        iptables -F OUTPUT 2>/dev/null || true
        
        # Remove any custom chains we might have created
        iptables -X 2>/dev/null || true
        
        # Set default policies to ACCEPT
        iptables -P INPUT ACCEPT 2>/dev/null || true
        iptables -P OUTPUT ACCEPT 2>/dev/null || true
        iptables -P FORWARD ACCEPT 2>/dev/null || true
    ' 2>/dev/null || warn "Failed to reset iptables for $container_name"
    
    info "Network conditions reset for $container_name"
}

# Function to restart all services
restart_all_services() {
    log "Restarting all services to ensure clean state..."
    
    cd ../docker
    
    # Stop all services
    docker-compose down 2>/dev/null || warn "Failed to stop some services"
    
    # Wait a moment
    sleep 2
    
    # Start all services
    docker-compose up -d 2>/dev/null || {
        error "Failed to restart services"
        return 1
    }
    
    log "All services restarted"
    
    # Wait for services to be healthy
    log "Waiting for services to become healthy..."
    local max_wait=60
    local wait_time=0
    
    while [ $wait_time -lt $max_wait ]; do
        local healthy_count=$(docker-compose ps | grep "Up" | wc -l)
        if [ $healthy_count -ge 5 ]; then  # Bootstrap + 3 sequencers + debugger
            log "All services are running"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Waiting for services... ($wait_time/${max_wait}s)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Not all services became healthy within ${max_wait}s"
        docker-compose ps
    fi
}

# Function to check and report final network status
check_final_status() {
    log "Checking final network status..."
    
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    local reset_success=true
    
    for container in "${containers[@]}"; do
        if docker exec "$container" echo "test" >/dev/null 2>&1; then
            # Check for remaining TC rules
            local tc_rules=$(docker exec "$container" tc qdisc show dev eth0 2>/dev/null | grep -v "pfifo_fast\|noqueue" | wc -l)
            
            # Check for remaining iptables DROP rules
            local iptables_drops=$(docker exec "$container" iptables -L OUTPUT 2>/dev/null | grep "DROP" | wc -l)
            
            if [ $tc_rules -eq 0 ] && [ $iptables_drops -eq 0 ]; then
                info "$container: ✅ Clean network state"
            else
                warn "$container: ⚠️  Some rules may remain (TC: $tc_rules, Drops: $iptables_drops)"
                reset_success=false
            fi
        else
            warn "$container: ❌ Not accessible"
            reset_success=false
        fi
    done
    
    if [ "$reset_success" = "true" ]; then
        log "🎉 All network conditions successfully reset!"
        info "Network simulation environment is ready for new tests"
    else
        warn "⚠️  Some network conditions may not have been fully reset"
        info "Consider using --restart flag for complete reset"
    fi
}

# Function to clean up any orphaned network namespaces or interfaces
cleanup_system_network() {
    log "Cleaning up system network artifacts..."
    
    # Clean up any orphaned Docker networks
    docker network prune -f >/dev/null 2>&1 || true
    
    # Clean up any leftover network namespaces (if we have permissions)
    ip netns list 2>/dev/null | grep -E "docker|p2p" | while read ns rest; do
        ip netns delete "$ns" 2>/dev/null || true
    done || true
    
    info "System network cleanup complete"
}

# Main execution
main() {
    log "Starting network reset procedure"
    
    if [ "$1" = "--restart" ]; then
        RESTART_SERVICES="true"
        log "Restart mode enabled"
    fi
    
    # Define containers to reset
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    if [ "$RESTART_SERVICES" = "true" ]; then
        restart_all_services
    else
        # Reset network conditions on all containers
        for container in "${containers[@]}"; do
            reset_container_network "$container" || true
        done
        
        # Start any stopped containers (from partition simulations)
        log "Starting any stopped containers..."
        cd ../docker
        docker-compose up -d >/dev/null 2>&1 || warn "Some services may already be running"
    fi
    
    # Clean up system-level network artifacts
    cleanup_system_network
    
    # Check final status
    check_final_status
    
    log "Network reset complete!"
    info "You can now run new network simulation tests"
}

# Function to show current network conditions before reset
show_current_conditions() {
    log "Current network conditions before reset:"
    
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    
    for container in "${containers[@]}"; do
        if docker exec "$container" echo "test" >/dev/null 2>&1; then
            info "=== $container ==="
            
            # Show TC rules
            local tc_output=$(docker exec "$container" tc qdisc show dev eth0 2>/dev/null | grep -v "noqueue" || echo "  No TC rules")
            echo "  TC Rules: $tc_output"
            
            # Show iptables drops
            local iptables_output=$(docker exec "$container" iptables -L OUTPUT 2>/dev/null | grep "DROP" || echo "  No DROP rules")
            echo "  IPTables: $iptables_output"
            
            echo
        else
            warn "$container: Not accessible"
        fi
    done
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Network Reset Tool

Usage: $0 [OPTIONS]

Options:
  --restart    - Completely restart all services (ensures clean state)
  -h, --help   - Show this help message

Description:
  Resets all network simulation conditions applied by:
  - simulate_latency.sh
  - simulate_packet_loss.sh  
  - simulate_partition.sh

This script will:
  1. Remove all traffic control (tc) rules
  2. Reset iptables rules
  3. Start any stopped containers
  4. Clean up system network artifacts
  5. Verify clean network state

Examples:
  $0                # Reset network conditions, keep services running
  $0 --restart      # Stop and restart all services for complete reset

EOF
    exit 0
fi

# Show current conditions if requested
if [ "$1" = "--show" ]; then
    show_current_conditions
    exit 0
fi

# Run main function
main "$@"