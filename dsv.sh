#!/bin/bash

# DSV - Decentralized Sequencer Validator Control Script
# Simplified and focused on production use

set -e

# Load environment variables if .env exists
if [ -f .env ]; then
    export $(grep -v '^#' .env | xargs)
fi

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
    echo "  start-all     - Start main services AND monitoring stack"
    echo "  stop          - Stop all services"
    echo "  restart       - Restart all services"
    echo "  status        - Show service status"
    echo "  clean         - Stop and remove all containers/volumes"
    echo ""
    echo "Monitoring Stack:"
    echo "  monitor-start - Start monitoring services (State Tracker + Enhanced API)"
    echo "  monitor-stop  - Stop monitoring services"
    echo "  monitor-status - Show monitoring services status"
    echo "  monitor-logs  - Show monitoring services logs"
    echo ""
    echo "Monitoring:"
    echo "  monitor       - Show pipeline status (legacy)"
    echo "  dashboard     - Open dashboard in browser (http://localhost:8080/swagger)"
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
        echo "Start monitoring: ./dsv.sh monitor-start"
        echo "View dashboard: ./dsv.sh dashboard"
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

    # Ask about monitoring stack
    if [ -f docker-compose.monitoring.yml ]; then
        if $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml ps --services 2>/dev/null | grep -q monitor-api; then
            print_color "$YELLOW" "Monitoring stack is running. Stop it too? (y/N)"
            read -p "" -n 1 -r
            echo
            if [[ $REPLY =~ ^[Yy]$ ]]; then
                stop_monitoring
            fi
        fi
    fi
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

# Start monitoring stack
start_monitoring() {
    print_color "$GREEN" "🔍 Starting Monitoring Stack"

    if [ ! -f docker-compose.monitoring.yml ]; then
        print_color "$RED" "Error: docker-compose.monitoring.yml not found"
        exit 1
    fi

    # Check if main services are running
    if ! is_separated_running; then
        print_color "$YELLOW" "Warning: Main DSV services are not running"
        print_color "$YELLOW" "Start them first with: ./dsv.sh start"
        echo ""
    fi

    $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml up -d state-tracker monitor-api

    if [ $? -eq 0 ]; then
        print_color "$GREEN" "✅ Monitoring stack started"
        echo ""
        print_color "$CYAN" "Services:"
        echo "  • State Tracker (tracks epoch/submission/validator states)"
        echo "  • Enhanced Monitor API (dashboard endpoints)"
        echo ""

        MONITOR_PORT="${MONITOR_API_PORT:-8080}"
        echo "Dashboard: http://localhost:${MONITOR_PORT}/swagger/index.html"
        echo "New endpoints:"
        echo "  • /api/v1/dashboard/summary - Overall health & performance"
        echo "  • /api/v1/epochs/timeline - Epoch progression tracking"
        echo "  • /api/v1/validator/performance - Validator metrics"
        echo "  • /api/v1/queues/analytics - Queue bottleneck detection"
        echo "  • /api/v1/network/consensus - Network consensus view"
    else
        print_color "$RED" "❌ Failed to start monitoring stack"
        exit 1
    fi
}

# Stop monitoring stack
stop_monitoring() {
    print_color "$YELLOW" "Stopping monitoring stack..."
    if [ -f docker-compose.monitoring.yml ]; then
        $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml down
        print_color "$GREEN" "✓ Monitoring stack stopped"
    else
        print_color "$RED" "Error: docker-compose.monitoring.yml not found"
    fi
}

# Show monitoring status
monitoring_status() {
    print_color "$BLUE" "Monitoring Stack Status:"
    echo ""
    if [ -f docker-compose.monitoring.yml ]; then
        $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml ps
    else
        print_color "$RED" "Error: docker-compose.monitoring.yml not found"
    fi
}

# Show monitoring logs
monitoring_logs() {
    if [ -f docker-compose.monitoring.yml ]; then
        $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml logs -f ${1:+--tail=$1}
    else
        print_color "$RED" "Error: docker-compose.monitoring.yml not found"
    fi
}

# Open dashboard
open_dashboard() {
    MONITOR_PORT="${MONITOR_API_PORT:-8080}"
    URL="http://localhost:${MONITOR_PORT}/swagger/index.html"

    print_color "$CYAN" "Opening dashboard at: $URL"

    # Check if monitor API is running
    if curl -s -o /dev/null -w "%{http_code}" "http://localhost:${MONITOR_PORT}/api/v1/health" | grep -q "200"; then
        print_color "$GREEN" "✓ Monitor API is running"

        # Try to open in browser
        if command -v open > /dev/null 2>&1; then
            open "$URL"
        elif command -v xdg-open > /dev/null 2>&1; then
            xdg-open "$URL"
        else
            print_color "$YELLOW" "Please open in your browser: $URL"
        fi
    else
        print_color "$YELLOW" "Monitor API is not running"
        print_color "$YELLOW" "Start it with: ./dsv.sh monitor-start"
    fi
}

# Monitor pipeline
monitor_pipeline() {
    if [ -f "scripts/monitor_api_client.sh" ]; then
        bash scripts/monitor_api_client.sh "${MONITOR_API_PORT}" "${PROTOCOL}" "${DATA_MARKET_ADDRESSES}"
    else
        print_color "$CYAN" "Monitor API: http://localhost:\${MONITOR_API_PORT:-8080}/swagger/index.html"
        print_color "$YELLOW" "Use the Monitor API service for pipeline monitoring"
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

        # Delete finalized batches using SCAN
        FINALIZED_COUNT=0
        docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT --scan --pattern '*:*:finalized:*' 2>/dev/null | while read key; do
            if [ ! -z "$key" ]; then
                docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT DEL "$key" >/dev/null 2>&1
                FINALIZED_COUNT=$((FINALIZED_COUNT + 1))
            fi
        done

        # Delete aggregated batches using SCAN
        AGG_COUNT=0
        docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:aggregated:*' 2>/dev/null | while read key; do
            if [ ! -z "$key" ]; then
                docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT DEL "$key" >/dev/null 2>&1
                AGG_COUNT=$((AGG_COUNT + 1))
            fi
        done

        # Delete batch parts using SCAN
        PARTS_COUNT=0
        docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT --scan --pattern '*:*:batch:part:*' 2>/dev/null | while read key; do
            if [ ! -z "$key" ]; then
                docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT DEL "$key" >/dev/null 2>&1
                PARTS_COUNT=$((PARTS_COUNT + 1))
            fi
        done

        # Clear namespaced aggregation queues using SCAN
        docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT --scan --pattern '*:*:aggregationQueue' 2>/dev/null | while read key; do
            [ ! -z "$key" ] && docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT DEL "$key" >/dev/null 2>&1
        done
        docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT --scan --pattern '*:*:aggregation:queue' 2>/dev/null | while read key; do
            [ ! -z "$key" ] && docker exec $REDIS_CONTAINER redis-cli -p $REDIS_PORT DEL "$key" >/dev/null 2>&1
        done

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
        stop_monitoring
        # Only remove volumes belonging to this project
        if is_separated_running || docker ps | grep -q snapshot-sequencer; then
            $DOCKER_COMPOSE_CMD -f docker-compose.separated.yml down -v 2>/dev/null || true
        fi
        if [ -f docker-compose.monitoring.yml ]; then
            $DOCKER_COMPOSE_CMD -f docker-compose.monitoring.yml down -v 2>/dev/null || true
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
    start-all)
        start_services
        sleep 2  # Give main services time to initialize
        start_monitoring
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
    monitor-start)
        start_monitoring
        ;;
    monitor-stop)
        stop_monitoring
        ;;
    monitor-status)
        monitoring_status
        ;;
    monitor-logs)
        monitoring_logs "$2"
        ;;
    dashboard)
        open_dashboard
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