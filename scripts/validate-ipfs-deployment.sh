#!/bin/bash

# IPFS Deployment Validation Script
# Validates that IPFS container is properly configured and running

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
IPFS_CONTAINER=${IPFS_CONTAINER:-dsv-ipfs}
CLEANUP_CONTAINER=${CLEANUP_CONTAINER:-dsv-ipfs-cleanup}
COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.separated.yml}

# Logging function
log() {
    echo -e "[$(date '+%Y-%m-%d %H:%M:%S')] ${1}"
}

# Check function
check() {
    local test_name="$1"
    local command="$2"

    log "${BLUE}Testing: $test_name${NC}"

    if eval "$command" > /dev/null 2>&1; then
        log "${GREEN}✓ $test_name${NC}"
        return 0
    else
        log "${RED}✗ $test_name${NC}"
        return 1
    fi
}

# Main validation
validate_ipfs_deployment() {
    log "${GREEN}🚀 Starting IPFS Deployment Validation${NC}"

    local errors=0

    # Check if docker-compose file exists
    if [ ! -f "$COMPOSE_FILE" ]; then
        log "${RED}❌ Docker compose file not found: $COMPOSE_FILE${NC}"
        return 1
    fi

    # Check containers
    log "${BLUE}Checking container status...${NC}"

    check "IPFS container running" "docker ps --filter name=$IPFS_CONTAINER --filter status=running --quiet | grep -q ."
    ((errors+=$?))

    check "IPFS cleanup container running" "docker ps --filter name=$CLEANUP_CONTAINER --filter status=running --quiet | grep -q ."
    ((errors+=$?))

    # Check container health
    log "${BLUE}Checking container health...${NC}"

    check "IPFS container healthy" "docker inspect $IPFS_CONTAINER --format='{{.State.Health.Status}}' | grep -q healthy"
    ((errors+=$?))

    # Check IPFS functionality
    log "${BLUE}Checking IPFS functionality...${NC}"

    check "IPFS daemon responding" "docker exec $IPFS_CONTAINER ipfs id > /dev/null"
    ((errors+=$?))

    check "IPFS API accessible" "docker exec $IPFS_CONTAINER curl -s http://localhost:5001/api/v0/version > /dev/null"
    ((errors+=$?))

    # Check configuration
    log "${BLUE}Checking IPFS configuration...${NC}"

    check "PubSub enabled" "docker exec $IPFS_CONTAINER ipfs config Pubsub.Enabled | grep -q true"
    ((errors+=$?))

    check "API bound to 0.0.0.0" "docker exec $IPFS_CONTAINER ipfs config Addresses.API | grep -q '0.0.0.0'"
    ((errors+=$?))

    check "Swarm configured" "docker exec $IPFS_CONTAINER ipfs config Addresses.Swarm | grep -q '0.0.0.0'"
    ((errors+=$?))

    # Check resource limits
    log "${BLUE}Checking resource limits...${NC}"

    check "High file descriptor limit" "[ \$(docker exec $IPFS_CONTAINER sh -c 'ulimit -n') -ge 100000 ]"
    ((errors+=$?))

    check "High process limit" "[ \$(docker exec $IPFS_CONTAINER sh -c 'ulimit -u') -ge 10000 ]"
    ((errors+=$?))

    # Check storage
    log "${BLUE}Checking storage configuration...${NC}"

    check "IPFS data directory exists" "docker exec $IPFS_CONTAINER test -d /data/ipfs"
    ((errors+=$?))

    check "IPFS repository initialized" "docker exec $IPFS_CONTAINER test -f /data/ipfs/config"
    ((errors+=$?))

    # Check cleanup service
    log "${BLUE}Checking cleanup service...${NC}"

    check "Cleanup script executable" "docker exec $CLEANUP_CONTAINER test -x /scripts/ipfs-cleanup.sh"
    ((errors+=$?))

    # Summary
    log "${BLUE}Validation completed.${NC}"

    if [ $errors -eq 0 ]; then
        log "${GREEN}🎉 All checks passed! IPFS deployment is properly configured.${NC}"

        # Show useful information
        log "${BLUE}IPFS Node Information:${NC}"
        docker exec $IPFS_CONTAINER ipfs id
        echo

        log "${BLUE}Repository Statistics:${NC}"
        docker exec $IPFS_CONTAINER ipfs repo stat
        echo

        log "${BLUE}Network Addresses:${NC}"
        docker exec $IPFS_CONTAINER ipfs swarm addrs local
        echo

        return 0
    else
        log "${RED}❌ $errors validation check(s) failed${NC}"
        log "${YELLOW}Please check the configuration and try again.${NC}"
        return 1
    fi
}

# Function to show detailed container information
show_container_info() {
    local container="$1"

    log "${BLUE}Container Information: $container${NC}"
    echo "Container ID: $(docker inspect $container --format='{{.Id}}' | cut -c1-12)"
    echo "Image: $(docker inspect $container --format='{{.Config.Image}}')"
    echo "Status: $(docker inspect $container --format='{{.State.Status}}')"
    echo "Health: $(docker inspect $container --format='{{.State.Health.Status}}')"
    echo "Uptime: $(docker inspect $container --format='{{.State.StartedAt}}')"
    echo
}

# Function to check host system configuration
check_host_config() {
    log "${BLUE}Checking host system configuration...${NC}"

    # Check file descriptor limits
    local fs_file_max=$(cat /proc/sys/fs/file-max 2>/dev/null || echo "N/A")
    log "File-max limit: $fs_file_max"

    # Check memory map limit
    local max_map_count=$(cat /proc/sys/vm/max_map_count 2>/dev/null || echo "N/A")
    log "Max map count: $max_map_count"

    # Check TCP buffer sizes
    local rmem_max=$(cat /proc/sys/net/core/rmem_max 2>/dev/null || echo "N/A")
    local wmem_max=$(cat /proc/sys/net/core/wmem_max 2>/dev/null || echo "N/A")
    log "TCP buffer sizes: rmem_max=$rmem_max, wmem_max=$wmem_max"
    echo
}

# Main execution
case "${1:-}" in
    --info)
        show_container_info "$IPFS_CONTAINER"
        show_container_info "$CLEANUP_CONTAINER"
        ;;
    --host)
        check_host_config
        ;;
    --all)
        check_host_config
        validate_ipfs_deployment
        ;;
    *)
        validate_ipfs_deployment
        ;;
esac