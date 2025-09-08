#!/bin/bash

# Unified Sequencer Launch Script
# This script provides easy launching of different sequencer configurations

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
COMPOSE_FILE="docker-compose.snapshot-sequencer.yml"
ENV_FILE=".env"

# Function to print colored output
print_color() {
    color=$1
    message=$2
    echo -e "${color}${message}${NC}"
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [COMMAND] [OPTIONS]"
    echo ""
    echo "Commands:"
    echo "  sequencer     - Launch all-in-one snapshot sequencer (all components hardcoded ON)"
    echo "  sequencer-custom - Launch snapshot sequencer with YOUR .env settings"
    echo "  distributed   - Launch distributed components (listener, dequeuer, finalizer)"
    echo "  distributed-debug - Launch distributed with Redis exposed"
    echo "  minimal       - Launch minimal setup (redis + unified)"
    echo "  full          - Launch full stack with monitoring"
    echo "  custom        - Launch with custom profile"
    echo "  stop          - Stop all services"
    echo "  clean         - Stop and remove all containers/volumes"
    echo "  logs          - Show logs for all services"
    echo "  event-monitor-logs    - Show event monitor logs (shortcut for event-monitor service)"
    echo "  status        - Show status of all services"
    echo "  monitor       - Monitor batch preparation status"
    echo "  debug         - Launch with Redis port exposed for debugging"
    echo ""
    echo "Options:"
    echo "  --debug       - Enable debug mode"
    echo "  --scale       - Scale services (e.g., --scale dequeuer=5)"
    echo "  --env         - Specify env file (default: .env)"
    echo "  --bootstrap   - Set bootstrap multiaddr"
    echo ""
    echo "Examples:"
    echo "  $0 sequencer                  # Run all-in-one sequencer"
    echo "  $0 distributed --debug        # Run distributed with debug"
    echo ""
    echo "Note: All commands automatically build Docker images before starting."
    echo "      No need to run build scripts separately."
}

# Function to check prerequisites
check_prerequisites() {
    if ! command -v docker &> /dev/null; then
        print_color "$RED" "Error: Docker is not installed"
        exit 1
    fi
    
    # Check for docker-compose (either standalone or plugin)
    if command -v docker-compose &> /dev/null; then
        DOCKER_COMPOSE_CMD="docker-compose"
    elif docker compose version &> /dev/null; then
        DOCKER_COMPOSE_CMD="docker compose"
    else
        print_color "$RED" "Error: Docker Compose is not installed"
        print_color "$YELLOW" "Install with: sudo apt install docker-compose"
        exit 1
    fi
    
    print_color "$GREEN" "✓ Prerequisites checked (using $DOCKER_COMPOSE_CMD)"
}

# Function to check env configuration
check_env_config() {
    if [ ! -f "$ENV_FILE" ]; then
        print_color "$YELLOW" "Warning: No .env file found"
        print_color "$YELLOW" "Copy .env.example to .env and configure it:"
        echo "  cp .env.example .env"
        echo "  nano .env"
        echo ""
        print_color "$YELLOW" "Continuing with environment variables if set..."
    fi
}

# Function to launch sequencer mode
launch_sequencer() {
    print_color "$BLUE" "Launching snapshot sequencer (all-in-one)..."
    
    # Build images first
    print_color "$YELLOW" "Building images..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" build
    
    # Start services
    print_color "$GREEN" "Starting sequencer..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --profile sequencer up -d
    
    print_color "$GREEN" "✓ Snapshot sequencer launched"
}

# Function to launch distributed mode
launch_distributed() {
    print_color "$BLUE" "Launching distributed sequencer with separate containers..."
    
    # Use the distributed compose file
    if [ ! -f "docker-compose.distributed.yml" ]; then
        print_color "$RED" "Error: docker-compose.distributed.yml not found"
        exit 1
    fi
    
    # Check if debug mode requested
    COMPOSE_ARGS="-f docker-compose.distributed.yml"
    if [ "$DEBUG_MODE" = "true" ]; then
        print_color "$YELLOW" "Debug mode enabled - Redis port will be exposed"
        COMPOSE_ARGS="$COMPOSE_ARGS -f docker-compose.debug.yml"
    fi
    
    # Build images first
    print_color "$YELLOW" "Building images..."
    $DOCKER_COMPOSE_CMD $COMPOSE_ARGS build
    
    # Start services
    print_color "$GREEN" "Starting distributed services..."
    $DOCKER_COMPOSE_CMD $COMPOSE_ARGS up -d
    
    # Wait for services to be ready
    sleep 3
    
    print_color "$GREEN" "✓ Distributed sequencer launched"
    echo ""
    echo "Components running:"
    echo "  - Listener: P2P on port ${P2P_PORT:-9001}"
    echo "  - Dequeuer: ${DEQUEUER_REPLICAS:-2} replicas"
    echo "  - Event Monitor: Watching for EpochReleased events"
    echo "  - Finalizer: ${FINALIZER_REPLICAS:-2} replicas"
    echo ""
    if [ "$DEBUG_MODE" = "true" ]; then
        echo "  🔍 DEBUG MODE: Redis exposed on localhost:6379"
        echo ""
    fi
    echo "Check status: docker-compose -f docker-compose.distributed.yml ps"
    echo "View logs: docker-compose -f docker-compose.distributed.yml logs -f [service-name]"
}


# Function to launch minimal setup
launch_minimal() {
    print_color "$BLUE" "Launching minimal setup..."
    
    # Build images first
    print_color "$YELLOW" "Building images..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" build
    
    # Start services
    print_color "$GREEN" "Starting minimal services..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" up -d redis sequencer-all
    
    print_color "$GREEN" "✓ Minimal setup launched"
}

# Function to launch full stack
launch_full() {
    print_color "$BLUE" "Launching full stack with monitoring..."
    
    # Build images first
    print_color "$YELLOW" "Building images..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" build
    
    # Start services
    print_color "$GREEN" "Starting full stack..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --profile distributed --profile storage --profile monitoring up -d
    
    print_color "$GREEN" "✓ Full stack launched"
    echo ""
    echo "Services available:"
    echo "  - Grafana: http://localhost:3000"
    echo "  - Prometheus: http://localhost:9090"
    echo "  - IPFS: http://localhost:8080"
}

# Function to launch custom profiles
launch_custom() {
    profiles=$1
    print_color "$BLUE" "Launching custom profiles: $profiles"
    
    IFS=',' read -ra PROFILE_ARRAY <<< "$profiles"
    profile_args=""
    for profile in "${PROFILE_ARRAY[@]}"; do
        profile_args="$profile_args --profile $profile"
    done
    
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" $profile_args up
    print_color "$GREEN" "✓ Custom configuration launched"
}

# Function to stop all services
stop_services() {
    print_color "$YELLOW" "Stopping all services..."
    
    # Check if distributed services are running
    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps --quiet 2>/dev/null | grep -q .; then
        print_color "$BLUE" "Stopping distributed services..."
        $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml down
    fi
    
    # Check if unified/standalone services are running
    if $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps --quiet 2>/dev/null | grep -q .; then
        print_color "$BLUE" "Stopping unified services..."
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" down
    fi
    
    print_color "$GREEN" "✓ All services stopped"
}

# Function to clean everything
clean_all() {
    print_color "$RED" "WARNING: This will remove all containers and volumes!"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        # Check and clean distributed services
        if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps --quiet 2>/dev/null | grep -q .; then
            print_color "$BLUE" "Cleaning distributed services..."
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml down -v
        fi
        
        # Check and clean regular services
        if $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps --quiet 2>/dev/null | grep -q .; then
            print_color "$BLUE" "Cleaning sequencer services..."
            $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" down -v
        fi
        
        print_color "$GREEN" "✓ Cleaned all containers and volumes"
    else
        print_color "$YELLOW" "Cancelled"
    fi
}

# Function to show logs
show_logs() {
    service=$1
    
    # Check which mode is running and use appropriate compose file
    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps --quiet 2>/dev/null | grep -q .; then
        # Distributed mode is running
        if [ -z "$service" ]; then
            print_color "$BLUE" "Streaming logs from distributed mode containers..."
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f
        else
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f "$service"
        fi
    elif $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps --quiet 2>/dev/null | grep -q .; then
        # Unified mode is running
        if [ -z "$service" ]; then
            $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f
        else
            $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f "$service"
        fi
    else
        print_color "$YELLOW" "No services are currently running"
    fi
}

# Function to monitor batch preparation
monitor_batches() {
    print_color "$BLUE" "Monitoring Batch Preparation Status..."
    
    # Find the first running sequencer container
    CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
    
    if [ -z "$CONTAINER" ]; then
        print_color "$RED" "No running sequencer container found"
        echo "Please start the sequencer first: $0 sequencer-custom"
        exit 1
    fi
    
    print_color "$GREEN" "Using container: $CONTAINER"
    echo ""
    
    # Execute monitoring inside the container
    docker exec -it $CONTAINER /bin/sh -c '
        REDIS_HOST="${REDIS_HOST:-redis}"
        REDIS_PORT="${REDIS_PORT:-6379}"
        
        echo "📊 Redis: $REDIS_HOST:$REDIS_PORT"
        echo "============================="
        
        # Current window
        echo -e "\n🔷 Current Submission Window:"
        redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "submission_window:current" 2>/dev/null || echo "  None active"
        
        # Ready batches
        echo -e "\n📦 Ready Batches:"
        READY=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:ready:*" 2>/dev/null)
        if [ ! -z "$READY" ]; then
            echo "$READY" | while read batch; do
                echo "  ✓ $batch"
            done
        else
            echo "  None"
        fi
        
        # Pending submissions
        echo -e "\n⏳ Pending Submissions:"
        COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "submissions:pending" 2>/dev/null)
        echo "  Count: ${COUNT:-0}"
        
        # Window statistics
        echo -e "\n📈 Window Statistics:"
        redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "window:*:submissions" 2>/dev/null | while read window; do
            if [ ! -z "$window" ]; then
                COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "$window" 2>/dev/null)
                echo "  $window: $COUNT submissions"
            fi
        done
        
        # Last preparation time
        echo -e "\n⏰ Last Batch Prepared:"
        LAST=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "batch:last_prepared_time" 2>/dev/null)
        echo "  ${LAST:-Never}"
    '
}

# Function to launch with debug mode (Redis exposed)
launch_debug() {
    print_color "$YELLOW" "Launching in DEBUG mode with Redis exposed..."
    
    # Build images first
    print_color "$YELLOW" "Building images..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" build
    
    # Use both compose files to start services
    print_color "$GREEN" "Starting services with debug mode..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" -f docker-compose.debug.yml up -d
    
    print_color "$GREEN" "✓ Debug mode enabled"
    echo ""
    echo "Redis is now accessible at: localhost:6379"
    echo "You can use: redis-cli -h localhost -p 6379"
    echo "Or run: ./scripts/check_batch_status.sh"
}

# Function to show status
show_status() {
    print_color "$BLUE" "Checking Service Status..."
    echo ""
    
    # Check distributed services
    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps 2>/dev/null | grep -q "Up"; then
        print_color "$GREEN" "Distributed Mode Services:"
        $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps
        echo ""
    fi
    
    # Check regular services
    if $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps 2>/dev/null | grep -q "Up"; then
        print_color "$GREEN" "Sequencer Services:"
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps
        echo ""
    fi
    
    # If no services found
    if ! $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps 2>/dev/null | grep -q "Up" && \
       ! $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps 2>/dev/null | grep -q "Up"; then
        print_color "$YELLOW" "No services are currently running"
        echo "Use './launch.sh sequencer-custom' or './launch.sh distributed' to start"
    fi
    
    echo ""
    print_color "$BLUE" "Redis Queue Status:"
    # Get the actual container name dynamically
    REDIS_CONTAINER=$($DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps -q redis 2>/dev/null)
    if [ -n "$REDIS_CONTAINER" ]; then
        docker exec -it "$REDIS_CONTAINER" redis-cli LLEN submissionQueue 2>/dev/null || echo "Queue not accessible"
    else
        echo "Redis container not found"
    fi
}

# Parse command line arguments
COMMAND=$1
shift

# Process options
while [[ $# -gt 0 ]]; do
    case $1 in
        --debug)
            export DEBUG_MODE=true
            shift
            ;;
        --scale)
            SCALE_ARG="--scale $2"
            shift 2
            ;;
        --env)
            ENV_FILE="$2"
            shift 2
            ;;
        --bootstrap)
            export BOOTSTRAP_MULTIADDR="$2"
            shift 2
            ;;
        *)
            EXTRA_ARGS="$1"
            shift
            ;;
    esac
done

# Main execution
check_prerequisites
check_env_config

# Load environment file
if [ -f "$ENV_FILE" ]; then
    set -a  # automatically export all variables
    source "$ENV_FILE"
    set +a  # turn off automatic export
fi

case $COMMAND in
    sequencer)
        launch_sequencer
        ;;
    sequencer-custom)
        print_color "$BLUE" "Launching snapshot sequencer with custom .env settings..."
        
        # Build images first
        print_color "$YELLOW" "Building images..."
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" build
        
        # Start services
        print_color "$GREEN" "Starting sequencer with custom configuration..."
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" up -d sequencer-custom
        
        print_color "$GREEN" "✓ Snapshot sequencer launched with your custom settings"
        ;;
    distributed)
        launch_distributed
        ;;
    distributed-debug)
        export DEBUG_MODE=true
        launch_distributed
        ;;
    minimal)
        launch_minimal
        ;;
    full)
        launch_full
        ;;
    custom)
        launch_custom "$EXTRA_ARGS"
        ;;
    stop)
        stop_services
        ;;
    clean)
        clean_all
        ;;
    logs)
        show_logs "$EXTRA_ARGS"
        ;;
    status)
        show_status
        ;;
    monitor)
        monitor_batches
        ;;
    event-monitor-logs)
        # Shortcut for viewing event monitor logs
        if is_distributed_mode; then
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f event-monitor
        else
            print_color "$YELLOW" "Event monitor only runs in distributed mode. Use: ./launch.sh distributed"
        fi
        ;;
    debug)
        launch_debug
        ;;
    *)
        show_usage
        exit 1
        ;;
esac