#!/bin/bash

# DSV - Decentralized Sequencer Validator Control Script
# Simplified and focused on production use

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Function to print colored output
print_color() {
    color=$1
    message=$2
    echo -e "${color}${message}${NC}"
}

# Detect docker compose command
if command -v "docker compose" &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker compose"
elif command -v docker-compose &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker-compose"
else
    print_color "$RED" "Error: docker compose not found"
    exit 1
fi

# Function to show usage
show_usage() {
    echo "DSV - Decentralized Sequencer Validator Control"
    echo ""
    echo "Usage: $0 <command> [options]"
    echo ""
    echo "Main Commands:"
    echo "  start         - Start separated architecture (production)"
    echo "  stop          - Stop all services"
    echo "  restart       - Restart all services"
    echo "  status        - Show service status"
    echo "  clean         - Stop and remove all containers/volumes"
    echo ""
    echo "Monitoring:"
    echo "  monitor       - Monitor pipeline status"
    echo "  logs          - Show all logs"
    echo "  p2p-logs      - P2P Gateway logs"
    echo "  aggregator-logs - Aggregator logs"
    echo "  finalizer-logs - Finalizer logs"
    echo "  dequeuer-logs - Dequeuer logs"
    echo "  event-logs    - Event monitor logs"
    echo "  redis-logs    - Redis logs"
    echo ""
    echo "Development:"
    echo "  build         - Build all binaries"
    echo "  dev           - Start unified sequencer (single container)"
}

# Check if separated mode is running
is_separated_running() {
    $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml ps --services 2>/dev/null | grep -q p2p-gateway
}

# Start production (separated) mode
start_services() {
    print_color "$GREEN" "🚀 Starting Separated Architecture"

    if [ ! -f docker-compose.separated.yml ]; then
        print_color "$RED" "Error: docker-compose.separated.yml not found"
        exit 1
    fi

    # Check environment
    if [ ! -f .env ]; then
        print_color "$YELLOW" "Warning: .env file not found. Using defaults."
    fi

    $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml up -d

    if [ $? -eq 0 ]; then
        print_color "$GREEN" "✅ Services started successfully"
        echo ""
        print_color "$CYAN" "Components:"
        echo "  • P2P Gateway (port ${P2P_PORT:-9001})"
        echo "  • Aggregator (consensus)"
        echo "  • Finalizer (batch creation)"
        echo "  • Dequeuer (submission processing)"
        echo "  • Event Monitor (epoch tracking)"
        echo ""
        echo "Monitor: ./dsv.sh monitor"
        echo "Logs: ./dsv.sh logs"
    else
        print_color "$RED" "❌ Failed to start services"
        exit 1
    fi
}

# Stop services
stop_services() {
    print_color "$YELLOW" "Stopping services..."
    if is_separated_running; then
        $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml down
    else
        # Try to stop any running containers
        $DOCKER_COMPOSE_CMD down 2>/dev/null || true
    fi
    print_color "$GREEN" "✓ Services stopped"
}

# Show status
show_status() {
    print_color "$BLUE" "Service Status:"
    echo ""
    if is_separated_running; then
        $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml ps
    else
        print_color "$YELLOW" "No services running"
    fi
}

# Monitor pipeline
monitor_pipeline() {
    print_color "$BLUE" "📊 Pipeline Status"
    echo ""

    # Try to connect via docker
    CONTAINER=$(docker ps --filter "name=redis" --format "{{.Names}}" | grep -E "redis-1|redis_1" | head -1)

    if [ -z "$CONTAINER" ]; then
        print_color "$RED" "Redis container not found"
        return 1
    fi

    REDIS_PORT=${REDIS_PORT:-6379}

    # Active windows
    print_color "$CYAN" "📂 Active Submission Windows:"
    docker exec $CONTAINER redis-cli -p $REDIS_PORT --raw KEYS "epoch:*:*:window" | while read key; do
        if [ ! -z "$key" ]; then
            STATUS=$(docker exec $CONTAINER redis-cli -p $REDIS_PORT GET "$key")
            TTL=$(docker exec $CONTAINER redis-cli -p $REDIS_PORT TTL "$key")
            echo "  • $key: $STATUS (TTL: ${TTL}s)"
        fi
    done | head -5 || echo "  None"

    # Queue depth
    echo ""
    print_color "$CYAN" "📥 Submission Queue:"
    COUNT=$(docker exec $CONTAINER redis-cli -p $REDIS_PORT LLEN submissionQueue)
    echo "  Pending: ${COUNT:-0} submissions"

    # Finalized batches
    echo ""
    print_color "$CYAN" "✅ Recent Finalized Batches:"
    docker exec $CONTAINER redis-cli -p $REDIS_PORT --raw KEYS "batch:finalized:*" | sort -rn | head -5 | while read key; do
        if [ ! -z "$key" ]; then
            EPOCH=${key##*:}
            echo "  • Epoch $EPOCH"
        fi
    done || echo "  None"
}

# Clean everything
clean_all() {
    print_color "$YELLOW" "⚠️  This will remove all containers and volumes"
    read -p "Continue? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        stop_services
        docker volume prune -f
        print_color "$GREEN" "✓ Cleanup complete"
    else
        print_color "$YELLOW" "Cancelled"
    fi
}

# Build binaries
build_binaries() {
    print_color "$BLUE" "Building binaries..."
    if [ -f build-binary.sh ]; then
        ./build-binary.sh
    else
        print_color "$RED" "build-binary.sh not found"
        exit 1
    fi
}

# Show logs for specific service
show_service_logs() {
    service=$1
    lines=${2:-100}

    if is_separated_running; then
        $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml logs -f --tail="$lines" "$service"
    else
        print_color "$YELLOW" "Services not running. Start with: ./dsv.sh start"
    fi
}

# Main command handler
case "${1:-}" in
    start|up)
        start_services
        ;;
    stop|down)
        stop_services
        ;;
    restart)
        stop_services
        sleep 2
        start_services
        ;;
    status|ps)
        show_status
        ;;
    monitor)
        monitor_pipeline
        ;;
    logs)
        if is_separated_running; then
            $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml logs -f ${2:+--tail=$2}
        else
            print_color "$YELLOW" "No services running"
        fi
        ;;
    p2p-logs)
        show_service_logs "p2p-gateway" "$2"
        ;;
    aggregator-logs)
        show_service_logs "aggregator" "$2"
        ;;
    finalizer-logs)
        show_service_logs "finalizer" "$2"
        ;;
    dequeuer-logs)
        show_service_logs "dequeuer" "$2"
        ;;
    event-logs)
        show_service_logs "event-monitor" "$2"
        ;;
    redis-logs)
        show_service_logs "redis" "$2"
        ;;
    clean)
        clean_all
        ;;
    build)
        build_binaries
        ;;
    dev)
        print_color "$BLUE" "Starting unified sequencer (development mode)..."
        $DOCKER_COMPOSE_CMD up -d
        ;;
    help|--help|-h|"")
        show_usage
        ;;
    *)
        print_color "$RED" "Unknown command: $1"
        echo ""
        show_usage
        exit 1
        ;;
esac