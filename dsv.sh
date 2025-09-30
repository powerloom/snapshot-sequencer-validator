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
if docker compose version &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker compose"
elif command -v docker-compose &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker-compose"
else
    print_color "$RED" "Error: Neither 'docker compose' nor 'docker-compose' found"
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

    $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml up -d --build

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
    # Use the comprehensive monitoring script
    if [ -f "scripts/monitor_pipeline.sh" ]; then
        bash scripts/monitor_pipeline.sh
    else
        print_color "$RED" "Error: scripts/monitor_pipeline.sh not found"
        return 1
    fi
}

# Clean Redis cache state (stale keys from old deployments)
clean_cache() {
    print_color "$BLUE" "🗑️  Cleaning Redis cache state"

    # Find Redis container
    REDIS_CONTAINER=$(docker ps --format "{{.Names}}" | grep -i redis | head -1)

    if [ -z "$REDIS_CONTAINER" ]; then
        print_color "$RED" "Error: Redis container not found"
        print_color "$YELLOW" "Start services first with: ./dsv.sh start"
        return 1
    fi

    print_color "$YELLOW" "This will flush stale keys from Redis:"
    echo "  - Finalized batches (*:*:finalized:*)"
    echo "  - Aggregated batches (*:*:batch:aggregated:*)"
    echo "  - Batch parts (*:*:batch:part:*)"
    echo "  - Aggregation queues"
    echo ""
    read -p "Continue? (y/N) " -n 1 -r
    echo

    if [[ $REPLY =~ ^[Yy]$ ]]; then
        # Get Redis port from container
        REDIS_PORT=$(docker exec "$REDIS_CONTAINER" sh -c 'echo $REDIS_PORT' 2>/dev/null)
        if [ -z "$REDIS_PORT" ]; then
            REDIS_PORT=6379
        fi

        # Delete finalized batches
        FINALIZED_COUNT=$(docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:finalized:*'" | wc -l)
        docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:finalized:*' | xargs -r redis-cli -p $REDIS_PORT DEL" 2>/dev/null

        # Delete aggregated batches
        AGG_COUNT=$(docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:aggregated:*'" | wc -l)
        docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:aggregated:*' | xargs -r redis-cli -p $REDIS_PORT DEL" 2>/dev/null

        # Delete batch parts
        PARTS_COUNT=$(docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:part:*'" | wc -l)
        docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:part:*' | xargs -r redis-cli -p $REDIS_PORT DEL" 2>/dev/null

        # Clear aggregation queues
        docker exec $REDIS_CONTAINER sh -c "redis-cli -p $REDIS_PORT DEL '*:*:aggregationQueue' '*:*:aggregation:queue'" 2>/dev/null

        print_color "$GREEN" "✓ Cache cleaned:"
        print_color "$GREEN" "  - Finalized batches: $FINALIZED_COUNT"
        print_color "$GREEN" "  - Aggregated batches: $AGG_COUNT"
        print_color "$GREEN" "  - Batch parts: $PARTS_COUNT"
    else
        print_color "$YELLOW" "Cancelled"
    fi
}

# Clean everything
clean_all() {
    print_color "$YELLOW" "⚠️  This will remove sequencer containers and volumes"
    read -p "Continue? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        stop_services
        # Only remove volumes belonging to this project
        if is_separated_running || docker ps | grep -q snapshot-sequencer; then
            $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml down -v 2>/dev/null || true
        fi
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
    clean-cache)
        clean_cache
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