#!/bin/bash

# DSV - Decentralized Sequencer Validator Control Script
# The main control interface for launching, monitoring, and managing DSV components
# Usage: ./dsv.sh [command] [options]

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
    echo "  listener-logs [N] - Show P2P listener logs (last N lines if specified)"
    echo "  dqr-logs [N]  - Show dequeuer worker logs (last N lines if specified)"
    echo "  finalizer-logs [N] - Show finalizer logs (last N lines if specified)"
    echo "  event-monitor-logs [N] - Show event monitor logs (last N lines if specified)"
    echo "  redis-logs [N]    - Show Redis logs (last N lines if specified)"
    echo "  status        - Show status of all services"
    echo "  monitor       - Monitor batch preparation status"
    echo "  pipeline      - Comprehensive pipeline monitoring (all stages)"
    echo "  consensus     - Show consensus/aggregation status"
    echo "  consensus-logs [N] - Show consensus-related logs (last N lines)"
    echo "  aggregated-batch [epoch] - Show complete local aggregation view"
    echo "  validator-details <id> [epoch] - Show specific validator's proposals"
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

# Function to check if distributed mode is running
is_distributed_mode() {
    # Check if distributed containers are running
    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps --services 2>/dev/null | grep -q listener; then
        return 0  # true - distributed mode is running
    else
        return 1  # false - distributed mode is not running
    fi
}

# Function to detect which mode is currently running
detect_running_mode() {
    # Check for distributed mode first
    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps --services 2>/dev/null | grep -q listener; then
        echo "docker-compose.distributed.yml"
    elif $DOCKER_COMPOSE_CMD -f docker-compose.snapshot-sequencer.yml ps --services 2>/dev/null | grep -q sequencer; then
        echo "docker-compose.snapshot-sequencer.yml"
    else
        echo ""
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
    
    # Detect the correct container name based on running mode
    if is_distributed_mode; then
        # For distributed mode, try event-monitor first, then other services
        CONTAINER=$(docker ps --filter "name=event-monitor" --format "{{.Names}}" | head -1)
        if [ -z "$CONTAINER" ]; then
            CONTAINER=$(docker ps --filter "name=dequeuer" --format "{{.Names}}" | head -1)
        fi
        if [ -z "$CONTAINER" ]; then
            CONTAINER=$(docker ps --filter "name=listener" --format "{{.Names}}" | head -1)
        fi
    else
        # For sequencer mode
        CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
    fi
    
    if [ -z "$CONTAINER" ]; then
        print_color "$RED" "No running sequencer container found"
        echo "Please start the sequencer first: $0 distributed or $0 sequencer"
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
        
        # Active submission windows (Updated for new format: epoch:market:epochID:window)
        echo -e "\n🔷 Active Submission Windows:"
        WINDOWS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "epoch:*:*:window" 2>/dev/null)
        if [ ! -z "$WINDOWS" ]; then
            echo "$WINDOWS" | while read window; do
                STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$window" 2>/dev/null)
                if [ "$STATUS" = "open" ]; then
                    # Parse epoch:market:epochID:window format
                    MARKET_EPOCH=$(echo "$window" | sed "s/epoch://;s/:window//" | sed "s/^[^:]*://" )
                    TTL=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT TTL "$window" 2>/dev/null)
                    echo "  ✓ Market-Epoch: $MARKET_EPOCH (TTL: ${TTL}s)"
                fi
            done
        else
            echo "  None active"
        fi
        
        # Submission queue depth
        echo -e "\n📥 Submission Queue:"
        COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "submissionQueue" 2>/dev/null)
        echo "  Pending: ${COUNT:-0} submissions"
        
        # Ready batches for finalization (Updated format: protocol:market:batch:ready:epochID)
        echo -e "\n📦 Ready Batches:"
        READY=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "*:*:batch:ready:*" 2>/dev/null)
        if [ ! -z "$READY" ]; then
            echo "$READY" | while read batch; do
                # Extract protocol:market and epoch from protocol:market:batch:ready:epochID
                PROTOCOL_MARKET=$(echo "$batch" | sed "s/:batch:ready:.*//")
                EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
                echo "  ✓ $PROTOCOL_MARKET - Epoch $EPOCH ready for finalization"
            done
        else
            echo "  None"
        fi
        
        # Finalized batches (Updated to look for batch:finalized:epochID keys)
        echo -e "\n✅ Finalized Batches (last 5):"
        FINALIZED=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "batch:finalized:*" 2>/dev/null | sort -rn | head -5)
        if [ ! -z "$FINALIZED" ]; then
            echo "$FINALIZED" | while read batch; do
                EPOCH=$(echo "$batch" | grep -oE "[0-9]+$")
                # Try to get additional metadata if available
                MERKLE=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT HGET "$batch" "merkle_root" 2>/dev/null)
                IPFS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT HGET "$batch" "ipfs_cid" 2>/dev/null)
                if [ ! -z "$MERKLE" ]; then
                    echo "  ✓ Epoch $EPOCH (Merkle: ${MERKLE:0:12}...)"
                else
                    echo "  ✓ Epoch $EPOCH"
                fi
            done
        else
            echo "  None"
        fi
        
        # Worker status
        echo -e "\n👷 Worker Status:"
        WORKERS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "worker:*:status" 2>/dev/null)
        if [ ! -z "$WORKERS" ]; then
            echo "$WORKERS" | while read worker; do
                STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$worker" 2>/dev/null)
                WORKER_NAME=$(echo "$worker" | sed "s/worker://;s/:status//")
                echo "  $WORKER_NAME: $STATUS"
            done
        else
            echo "  No active workers"
        fi
        
        # Statistics
        echo -e "\n📈 Statistics:"
        PROCESSED=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT SCARD "*:processed" 2>/dev/null | head -1)
        echo "  Processed submissions: ${PROCESSED:-0}"
        
        QUEUE_DEPTH=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT LLEN "submissionQueue" 2>/dev/null)
        echo "  Current queue depth: ${QUEUE_DEPTH:-0}"
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

    # Check which mode is running to avoid duplication
    DISTRIBUTED_RUNNING=false
    SEQUENCER_RUNNING=false

    if $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps 2>/dev/null | grep -q "Up"; then
        DISTRIBUTED_RUNNING=true
    fi

    if $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps 2>/dev/null | grep -q "Up"; then
        SEQUENCER_RUNNING=true
    fi

    # Show appropriate status based on what's running
    if [ "$DISTRIBUTED_RUNNING" = true ]; then
        print_color "$GREEN" "Distributed Mode Services:"
        $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml ps
        echo ""
    elif [ "$SEQUENCER_RUNNING" = true ]; then
        print_color "$GREEN" "Sequencer Services:"
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps
        echo ""
    else
        print_color "$YELLOW" "No services are currently running"
        echo "Use './dsv.sh sequencer-custom' or './dsv.sh distributed' to start"
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
        # Try the simple monitor first (more reliable)
        if [ -f "./scripts/monitor_simple.sh" ]; then
            ./scripts/monitor_simple.sh
        else
            # Fallback to container-based monitoring
            monitor_batches
        fi
        ;;
    batch-details)
        # Show detailed batch metadata including IPFS CIDs
        # Usage: ./dsv.sh batch-details [epoch_id]
        if [ -f "./scripts/batch_details.sh" ]; then
            ./scripts/batch_details.sh "$2"
        else
            print_color "$RED" "Error: batch_details.sh script not found"
            exit 1
        fi
        ;;
    listener-logs)
        # Shortcut for viewing P2P listener logs
        # Usage: ./dsv.sh listener-logs [number_of_lines]
        if is_distributed_mode; then
            LINES="${2:-}"
            if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" listener
            else
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f listener
            fi
        else
            print_color "$YELLOW" "Listener only runs in distributed mode. Use: ./dsv.sh distributed"
        fi
        ;;
    dqr-logs|dequeuer-logs)
        # Shortcut for viewing dequeuer worker logs
        # Usage: ./dsv.sh dqr-logs [number_of_lines]
        if is_distributed_mode; then
            LINES="${2:-}"
            if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" dequeuer
            else
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f dequeuer
            fi
        else
            print_color "$YELLOW" "Dequeuer only runs in distributed mode. Use: ./dsv.sh distributed"
        fi
        ;;
    finalizer-logs)
        # Shortcut for viewing finalizer logs
        # Usage: ./dsv.sh finalizer-logs [number_of_lines]
        if is_distributed_mode; then
            LINES="${2:-}"
            if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" finalizer
            else
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f finalizer
            fi
        else
            print_color "$YELLOW" "Finalizer only runs in distributed mode. Use: ./dsv.sh distributed"
        fi
        ;;
    event-monitor-logs)
        # Shortcut for viewing event monitor logs
        # Usage: ./dsv.sh event-monitor-logs [number_of_lines]
        if is_distributed_mode; then
            LINES="${2:-}"
            if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" event-monitor
            else
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f event-monitor
            fi
        else
            print_color "$YELLOW" "Event monitor only runs in distributed mode. Use: ./dsv.sh distributed"
        fi
        ;;
    redis-logs)
        # Shortcut for viewing Redis logs
        # Usage: ./dsv.sh redis-logs [number_of_lines]
        LINES="${2:-}"
        if is_distributed_mode; then
            if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" redis
            else
                $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f redis
            fi
        else
            # Try to show Redis logs from any compose file that's running
            COMPOSE_FILE=$(detect_running_mode)
            if [ ! -z "$COMPOSE_FILE" ]; then
                if [ ! -z "$LINES" ] && [ "$LINES" -eq "$LINES" ] 2>/dev/null; then
                    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f --tail="$LINES" redis
                else
                    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f redis
                fi
            else
                print_color "$YELLOW" "No Redis service is currently running"
            fi
        fi
        ;;
    pipeline)
        # Comprehensive pipeline monitoring
        print_color "$CYAN" "🔍 Launching Comprehensive Pipeline Monitor..."
        
        # Detect the correct container name based on running mode
        if is_distributed_mode; then
            # For distributed mode, use docker ps to find running containers
            CONTAINER_NAME=$(docker ps --format "{{.Names}}" | grep -E "listener|dequeuer|finalizer|event-monitor" | head -1)
        else
            # For sequencer mode
            CONTAINER_NAME=$(docker ps --format "{{.Names}}" | grep sequencer | head -1)
        fi
        
        if [ -z "$CONTAINER_NAME" ]; then
            print_color "$RED" "Error: No running sequencer containers found"
            print_color "$YELLOW" "Start the sequencer first with: ./dsv.sh sequencer or ./dsv.sh distributed"
            exit 1
        fi
        
        ./scripts/monitor_pipeline.sh "$CONTAINER_NAME"
        ;;
    
    collection-logs)
        # View collection pipeline logs (dequeuer + event-monitor)
        print_color "$BLUE" "📦 Viewing collection pipeline logs (dequeuer + event-monitor)..."
        LINES="${2:-100}"
        if is_distributed_mode; then
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" dequeuer event-monitor
        else
            COMPOSE_FILE=$(detect_running_mode)
            if [ ! -z "$COMPOSE_FILE" ]; then
                $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f --tail="$LINES" snapshot-sequencer-validator-dequeuer snapshot-sequencer-validator-event-monitor
            else
                print_color "$YELLOW" "No services appear to be running"
                exit 1
            fi
        fi
        ;;
    
    finalization-logs)
        # View finalization pipeline logs (event-monitor + finalizer)
        print_color "$BLUE" "🎯 Viewing finalization pipeline logs (event-monitor + finalizer)..."
        LINES="${2:-100}"
        if is_distributed_mode; then
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" event-monitor finalizer
        else
            COMPOSE_FILE=$(detect_running_mode)
            if [ ! -z "$COMPOSE_FILE" ]; then
                $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f --tail="$LINES" snapshot-sequencer-validator-event-monitor snapshot-sequencer-validator-finalizer
            else
                print_color "$YELLOW" "No services appear to be running"
                exit 1
            fi
        fi
        ;;
    
    pipeline-logs)
        # View full pipeline logs (dequeuer + event-monitor + finalizer)
        print_color "$BLUE" "🔄 Viewing full pipeline logs (dequeuer + event-monitor + finalizer)..."
        LINES="${2:-100}"
        if is_distributed_mode; then
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs -f --tail="$LINES" dequeuer event-monitor finalizer
        else
            COMPOSE_FILE=$(detect_running_mode)
            if [ ! -z "$COMPOSE_FILE" ]; then
                $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f --tail="$LINES" snapshot-sequencer-validator-dequeuer snapshot-sequencer-validator-event-monitor snapshot-sequencer-validator-finalizer
            else
                print_color "$YELLOW" "No services appear to be running"
                exit 1
            fi
        fi
        ;;
    consensus)
        # Show consensus/aggregation status
        print_color "$BLUE" "🤝 Consensus/Aggregation Status"
        print_color "$BLUE" "================================"

        # Auto-detect container
        CONTAINER="${2:-}"
        if [ -z "$CONTAINER" ]; then
            CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
            if [ -z "$CONTAINER" ]; then
                CONTAINER=$(docker ps --filter "name=listener" --format "{{.Names}}" | head -1)
            fi
            if [ -z "$CONTAINER" ]; then
                print_color "$RED" "Error: No running sequencer containers found"
                print_color "$YELLOW" "Start the sequencer first with: ./dsv.sh sequencer or ./dsv.sh distributed"
                exit 1
            fi
        fi

        docker exec $CONTAINER /bin/sh -c '
            REDIS_HOST="${REDIS_HOST:-redis}"
            REDIS_PORT="${REDIS_PORT:-6379}"

            echo "📊 Batch Aggregation Status"
            echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

            # Check for aggregation status keys
            echo -e "\n🔄 Recent Aggregation Status:"
            AGG_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "consensus:epoch:*:status" 2>/dev/null | head -10)
            if [ ! -z "$AGG_KEYS" ]; then
                echo "$AGG_KEYS" | while read key; do
                    if [ ! -z "$key" ]; then
                        EPOCH=$(echo "$key" | grep -oE "[0-9]+" | head -1)
                        STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$key" 2>/dev/null)
                        if [ ! -z "$STATUS" ] && command -v jq >/dev/null 2>&1; then
                            VALIDATORS=$(echo "$STATUS" | jq -r ".ReceivedBatches" 2>/dev/null)
                            PROJECTS=$(echo "$STATUS" | jq -r ".AggregatedProjects | length" 2>/dev/null)
                            echo "  📦 Epoch $EPOCH: $VALIDATORS validators, $PROJECTS projects aggregated"
                        else
                            echo "  📦 Epoch $EPOCH: Status stored"
                        fi
                    fi
                done
            else
                echo "  ⚫ No aggregation status found"
            fi

            # Check for consensus results
            echo -e "\n🎯 Consensus Results:"
            RESULT_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "consensus:epoch:*:result" 2>/dev/null | head -10)
            if [ ! -z "$RESULT_KEYS" ]; then
                echo "$RESULT_KEYS" | while read key; do
                    if [ ! -z "$key" ]; then
                        EPOCH=$(echo "$key" | grep -oE "[0-9]+" | head -1)
                        RESULT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "$key" 2>/dev/null)
                        if [ ! -z "$RESULT" ] && command -v jq >/dev/null 2>&1; then
                            CID=$(echo "$RESULT" | jq -r ".cid" 2>/dev/null)
                            MERKLE=$(echo "$RESULT" | jq -r ".merkle_root" 2>/dev/null | cut -c1-16)
                            PROJECTS=$(echo "$RESULT" | jq -r ".projects" 2>/dev/null)
                            echo "  ✅ Epoch $EPOCH: CID=$CID, Merkle=${MERKLE}..., Projects=$PROJECTS"
                        else
                            echo "  ✅ Epoch $EPOCH: Result ready"
                        fi
                    fi
                done
            else
                echo "  ⚫ No consensus results yet"
            fi

            # Check validator batches
            echo -e "\n📨 Validator Batches Received:"
            VAL_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT KEYS "validator:*:epoch:*:batch" 2>/dev/null | head -10)
            if [ ! -z "$VAL_KEYS" ]; then
                VALIDATOR_COUNT=$(echo "$VAL_KEYS" | cut -d: -f2 | sort -u | wc -l)
                EPOCH_COUNT=$(echo "$VAL_KEYS" | grep -oE "epoch:[0-9]+" | cut -d: -f2 | sort -u | wc -l)
                echo "  📊 $VALIDATOR_COUNT validators across $EPOCH_COUNT epochs"
            else
                echo "  ⚫ No validator batches received"
            fi
        '
        ;;

    consensus-logs)
        # Show consensus-related logs
        print_color "$BLUE" "📜 Consensus/Aggregation Logs"
        LINES="${2:-100}"

        if is_distributed_mode; then
            # In distributed mode, check listener for consensus logs
            print_color "$YELLOW" "Showing consensus activity from listener..."
            $DOCKER_COMPOSE_CMD -f docker-compose.distributed.yml logs --tail="$LINES" listener | grep -E "consensus|aggregat|AGGREGATION|Consensus|validators|batch exchange" || echo "No consensus logs found"
        else
            # In unified mode
            COMPOSE_FILE=$(detect_running_mode)
            if [ ! -z "$COMPOSE_FILE" ]; then
                print_color "$YELLOW" "Showing consensus activity..."
                $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs --tail="$LINES" | grep -E "consensus|aggregat|AGGREGATION|Consensus|validators|batch exchange" || echo "No consensus logs found"
            else
                print_color "$YELLOW" "No services appear to be running"
                exit 1
            fi
        fi
        ;;

    validator-details)
        # Show details for a specific validator's proposals
        VALIDATOR_ID="${2:-}"
        EPOCH_ID="${3:-}"

        if [ -z "$VALIDATOR_ID" ]; then
            print_color "$RED" "Error: Validator ID required"
            print_color "$YELLOW" "Usage: ./dsv.sh validator-details <validator_id> [epoch_id]"
            exit 1
        fi

        print_color "$BLUE" "👤 Validator Details: $VALIDATOR_ID"
        print_color "$BLUE" "===================================="

        # Auto-detect container
        CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
        if [ -z "$CONTAINER" ]; then
            CONTAINER=$(docker ps --filter "name=listener" --format "{{.Names}}" | head -1)
        fi
        if [ -z "$CONTAINER" ]; then
            print_color "$RED" "Error: No running sequencer containers found"
            exit 1
        fi

        docker exec $CONTAINER /bin/sh -c "
            REDIS_HOST=\"\${REDIS_HOST:-redis}\"
            REDIS_PORT=\"\${REDIS_PORT:-6379}\"
            VALIDATOR_ID=\"$VALIDATOR_ID\"
            EPOCH_ID=\"$EPOCH_ID\"

            if [ -z \"\$EPOCH_ID\" ]; then
                # Show all epochs for this validator
                echo \"📦 All Batches from Validator \$VALIDATOR_ID:\"
                echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"

                BATCH_KEYS=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT KEYS \"validator:\$VALIDATOR_ID:batch:*\" 2>/dev/null | sort -rn)
                if [ -z \"\$BATCH_KEYS\" ]; then
                    echo \"⚫ No batches found for validator \$VALIDATOR_ID\"
                    exit 0
                fi

                echo \"\$BATCH_KEYS\" | while read key; do
                    if [ ! -z \"\$key\" ]; then
                        EPOCH=\$(echo \"\$key\" | grep -oE '[0-9]+$')
                        BATCH_DATA=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"\$key\" 2>/dev/null)

                        if [ ! -z \"\$BATCH_DATA\" ] && command -v jq >/dev/null 2>&1; then
                            echo -e \"\\n🔹 Epoch \$EPOCH:\"
                            echo \"  IPFS CID: \$(echo \"\$BATCH_DATA\" | jq -r '.batch_ipfs_cid')\"
                            echo \"  Merkle Root: \$(echo \"\$BATCH_DATA\" | jq -r '.merkle_root' | cut -c1-32)...\"
                            echo \"  Projects: \$(echo \"\$BATCH_DATA\" | jq -r '.project_count')\"
                            echo \"  Timestamp: \$(echo \"\$BATCH_DATA\" | jq -r '.timestamp')\"

                            # Show project proposals
                            VOTES=\$(echo \"\$BATCH_DATA\" | jq -r '.metadata.votes // empty' 2>/dev/null)
                            if [ ! -z \"\$VOTES\" ] && [ \"\$VOTES\" != \"null\" ]; then
                                echo \"  Project Proposals:\"
                                echo \"\$VOTES\" | jq -r 'to_entries[] | \"    - \" + .key + \": \" + (.value.cid // .value // \"unknown\")' 2>/dev/null | head -10
                            fi
                        fi
                    fi
                done
            else
                # Show specific epoch for this validator
                echo \"📦 Validator \$VALIDATOR_ID - Epoch \$EPOCH_ID Details:\"
                echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"

                BATCH_DATA=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"validator:\$VALIDATOR_ID:batch:\$EPOCH_ID\" 2>/dev/null)

                if [ -z \"\$BATCH_DATA\" ]; then
                    echo \"⚫ No batch found for validator \$VALIDATOR_ID in epoch \$EPOCH_ID\"
                    exit 0
                fi

                if command -v jq >/dev/null 2>&1; then
                    echo -e \"\\n📋 BATCH METADATA:\"
                    echo \"  IPFS CID: \$(echo \"\$BATCH_DATA\" | jq -r '.batch_ipfs_cid')\"
                    echo \"  Merkle Root: \$(echo \"\$BATCH_DATA\" | jq -r '.merkle_root')\"
                    echo \"  Project Count: \$(echo \"\$BATCH_DATA\" | jq -r '.project_count')\"
                    echo \"  Peer ID: \$(echo \"\$BATCH_DATA\" | jq -r '.peer_id')\"
                    echo \"  Timestamp: \$(echo \"\$BATCH_DATA\" | jq -r '.timestamp')\"
                    echo \"  Signature: \$(echo \"\$BATCH_DATA\" | jq -r '.signature' | cut -c1-32)...\"

                    echo -e \"\\n🗳️ PROJECT PROPOSALS:\"
                    VOTES=\$(echo \"\$BATCH_DATA\" | jq -r '.metadata.votes // empty' 2>/dev/null)
                    if [ ! -z \"\$VOTES\" ] && [ \"\$VOTES\" != \"null\" ]; then
                        echo \"\$VOTES\" | jq -r 'to_entries[] | \"  \" + .key + \": \" + (.value.cid // .value // \"unknown\")' 2>/dev/null
                    else
                        echo \"  ⚫ No project proposals in metadata\"
                    fi

                    # Check if this validator's proposals made it to consensus
                    echo -e \"\\n✅ CONSENSUS COMPARISON:\"
                    STATUS=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"consensus:epoch:\$EPOCH_ID:status\" 2>/dev/null)
                    if [ ! -z \"\$STATUS\" ]; then
                        AGGREGATED=\$(echo \"\$STATUS\" | jq -r '.AggregatedProjects // empty' 2>/dev/null)
                        if [ ! -z \"\$AGGREGATED\" ] && [ \"\$AGGREGATED\" != \"null\" ]; then
                            echo \"  Projects that reached consensus:\"
                            if [ ! -z \"\$VOTES\" ] && [ \"\$VOTES\" != \"null\" ]; then
                                echo \"\$VOTES\" | jq -r --argjson agg \"\$AGGREGATED\" '
                                    to_entries[] |
                                    . as \$vote |
                                    if \$agg[.key] == (.value.cid // .value) then
                                        \"    ✅ \" + .key + \" - ACCEPTED\"
                                    else
                                        \"    ❌ \" + .key + \" - Different CID won\"
                                    end' 2>/dev/null | head -20
                            fi
                        fi
                    else
                        echo \"  ⚫ No consensus data available for comparison\"
                    fi
                else
                    echo \"Batch data exists but cannot parse (jq not available)\"
                fi
            fi
        "
        ;;

    aggregated-batch)
        # Show details of the most recent aggregated batch
        EPOCH_ID="${2:-}"
        print_color "$BLUE" "📊 Complete Local Aggregation View"
        print_color "$BLUE" "==================================="

        # Auto-detect container
        CONTAINER=$(docker ps --filter "name=sequencer" --format "{{.Names}}" | head -1)
        if [ -z "$CONTAINER" ]; then
            CONTAINER=$(docker ps --filter "name=listener" --format "{{.Names}}" | head -1)
        fi
        if [ -z "$CONTAINER" ]; then
            print_color "$RED" "Error: No running sequencer containers found"
            exit 1
        fi

        docker exec $CONTAINER /bin/sh -c "
            REDIS_HOST=\"\${REDIS_HOST:-redis}\"
            REDIS_PORT=\"\${REDIS_PORT:-6379}\"
            EPOCH_ID=\"$EPOCH_ID\"

            if [ -z \"\$EPOCH_ID\" ]; then
                # Find the most recent aggregated epoch
                LATEST_KEY=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT KEYS 'consensus:epoch:*:status' 2>/dev/null | sort -rn | head -1)
                if [ ! -z \"\$LATEST_KEY\" ]; then
                    EPOCH_ID=\$(echo \"\$LATEST_KEY\" | grep -oE '[0-9]+' | head -1)
                fi
            fi

            if [ -z \"\$EPOCH_ID\" ]; then
                echo '⚫ No aggregated batches found'
                exit 0
            fi

            echo \"📦 Epoch \$EPOCH_ID - Complete Aggregation View\"
            echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"

            # Get all validator batches for this epoch
            echo -e \"\\n🔍 INDIVIDUAL VALIDATOR BATCHES:\"
            echo \"─────────────────────────────────\"
            VALIDATOR_KEYS=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT KEYS \"validator:*:batch:\$EPOCH_ID\" 2>/dev/null)
            if [ ! -z \"\$VALIDATOR_KEYS\" ]; then
                echo \"\$VALIDATOR_KEYS\" | while read key; do
                    if [ ! -z \"\$key\" ]; then
                        VALIDATOR=\$(echo \"\$key\" | cut -d: -f2)
                        BATCH_DATA=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"\$key\" 2>/dev/null)
                        if [ ! -z \"\$BATCH_DATA\" ] && command -v jq >/dev/null 2>&1; then
                            IPFS_CID=\$(echo \"\$BATCH_DATA\" | jq -r '.batch_ipfs_cid' 2>/dev/null)
                            MERKLE=\$(echo \"\$BATCH_DATA\" | jq -r '.merkle_root' 2>/dev/null | cut -c1-12)
                            PROJECT_COUNT=\$(echo \"\$BATCH_DATA\" | jq -r '.project_count' 2>/dev/null)
                            TIMESTAMP=\$(echo \"\$BATCH_DATA\" | jq -r '.timestamp' 2>/dev/null)
                            PEER_ID=\$(echo \"\$BATCH_DATA\" | jq -r '.peer_id' 2>/dev/null | cut -c1-8)

                            echo \"\\n📨 Validator: \$VALIDATOR\"
                            echo \"   IPFS CID: \$IPFS_CID\"
                            echo \"   Merkle: \${MERKLE}...\"
                            echo \"   Projects: \$PROJECT_COUNT\"
                            echo \"   PeerID: \${PEER_ID}...\"
                            echo \"   Timestamp: \$TIMESTAMP\"

                            # Show what projects this validator voted for
                            VOTES=\$(echo \"\$BATCH_DATA\" | jq -r '.metadata.votes // empty' 2>/dev/null)
                            if [ ! -z \"\$VOTES\" ] && [ \"\$VOTES\" != \"null\" ]; then
                                echo \"   Project Proposals:\"
                                echo \"\$VOTES\" | jq -r 'to_entries | .[:5][] | \"     - \" + .key + \": \" + (.value.cid // .value)[0:12] + \"...\"' 2>/dev/null
                            fi
                        else
                            echo \"  Validator \$VALIDATOR: Data stored (parse failed)\"
                        fi
                    fi
                done
            else
                echo \"  ⚫ No validator batches received for this epoch\"
            fi

            # Get aggregation status
            echo -e \"\\n📊 LOCAL AGGREGATION RESULTS:\"
            echo \"─────────────────────────────\"
            STATUS=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"consensus:epoch:\$EPOCH_ID:status\" 2>/dev/null)
            if [ ! -z \"\$STATUS\" ] && command -v jq >/dev/null 2>&1; then
                echo \"  Total Validators Participated: \$(echo \"\$STATUS\" | jq -r '.ReceivedBatches')\"
                echo \"  Projects with Consensus: \$(echo \"\$STATUS\" | jq -r '.AggregatedProjects | length')\"
                echo \"  Last Updated: \$(echo \"\$STATUS\" | jq -r '.UpdatedAt')\"

                echo -e \"\\n🗳️ DETAILED VOTE DISTRIBUTION:\"
                echo \"─────────────────────────────\"
                # Show vote details per project
                echo \"\$STATUS\" | jq -r '
                    .ProjectVotes | to_entries[:10][] |
                    \"\\n  Project: \" + .key +
                    \"\\n\" + (.value | to_entries |
                    map(\"    CID \" + .key[0:12] + \"...: \" + (.value | tostring) + \" validator(s)\") |
                    join(\"\\n\"))' 2>/dev/null

                echo -e \"\\n✅ CONSENSUS WINNERS:\"
                echo \"─────────────────────\"
                echo \"\$STATUS\" | jq -r '.AggregatedProjects | to_entries[:10][] | \"  \" + .key + \": \" + .value[0:16] + \"...\"' 2>/dev/null

                echo -e \"\\n👥 VALIDATOR CONTRIBUTIONS:\"
                echo \"───────────────────────────\"
                echo \"\$STATUS\" | jq -r '
                    .ValidatorContributions | to_entries[] |
                    \"  \" + .key + \": \" + (.value | length | tostring) + \" projects contributed\"' 2>/dev/null
            else
                echo '  Unable to parse aggregation status (jq not available or no data)'
            fi

            # Get consensus result
            RESULT=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"consensus:epoch:\$EPOCH_ID:result\" 2>/dev/null)
            if [ ! -z \"\$RESULT\" ] && command -v jq >/dev/null 2>&1; then
                echo -e \"\\n🎯 LOCAL CONSENSUS DETERMINATION:\"
                echo \"──────────────────────────────────\"
                echo \"  Aggregated by: \$(echo \"\$RESULT\" | jq -r '.validator_id')\"
                echo \"  Consensus Merkle Root: \$(echo \"\$RESULT\" | jq -r '.merkle_root' | cut -c1-32)...\"
                echo \"  Projects in Consensus: \$(echo \"\$RESULT\" | jq -r '.projects')\"
                echo \"  Determined at: \$(echo \"\$RESULT\" | jq -r '.timestamp')\"
            fi

            # Check which validators are in the epoch set
            echo -e \"\\n📋 EPOCH VALIDATOR SET:\"
            echo \"───────────────────────\"
            VALIDATORS=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT SMEMBERS \"epoch:\$EPOCH_ID:validators\" 2>/dev/null)
            if [ ! -z \"\$VALIDATORS\" ]; then
                echo \"\$VALIDATORS\" | tr ' ' '\\n' | while read validator; do
                    if [ ! -z \"\$validator\" ]; then
                        echo \"  ✓ \$validator\"
                    fi
                done
            else
                echo \"  ⚫ No validator set tracked\"
            fi
        "
        ;;

    debug)
        launch_debug
        ;;
    *)
        show_usage
        exit 1
        ;;
esac