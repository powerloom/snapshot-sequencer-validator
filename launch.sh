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
    echo "  minimal       - Launch minimal setup (redis + unified)"
    echo "  full          - Launch full stack with monitoring"
    echo "  custom        - Launch with custom profile"
    echo "  stop          - Stop all services"
    echo "  clean         - Stop and remove all containers/volumes"
    echo "  logs          - Show logs for all services"
    echo "  status        - Show status of all services"
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
    echo "  $0 custom listener,dequeuer   # Run specific components"
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
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --profile sequencer up -d
    print_color "$GREEN" "✓ Snapshot sequencer launched"
}

# Function to launch distributed mode
launch_distributed() {
    print_color "$BLUE" "Launching distributed sequencer..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --profile distributed up -d
    print_color "$GREEN" "✓ Distributed sequencer launched"
    echo ""
    echo "Components running:"
    echo "  - Listener: Port 9100"
    echo "  - Dequeuer: 3 replicas"
    echo "  - Finalizer: 2 replicas"
}


# Function to launch minimal setup
launch_minimal() {
    print_color "$BLUE" "Launching minimal setup..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" up -d redis sequencer-all
    print_color "$GREEN" "✓ Minimal setup launched"
}

# Function to launch full stack
launch_full() {
    print_color "$BLUE" "Launching full stack with monitoring..."
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
    
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" $profile_args up -d
    print_color "$GREEN" "✓ Custom configuration launched"
}

# Function to stop all services
stop_services() {
    print_color "$YELLOW" "Stopping all services..."
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" down
    print_color "$GREEN" "✓ Services stopped"
}

# Function to clean everything
clean_all() {
    print_color "$RED" "WARNING: This will remove all containers and volumes!"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" down -v
        print_color "$GREEN" "✓ Cleaned all containers and volumes"
    else
        print_color "$YELLOW" "Cancelled"
    fi
}

# Function to show logs
show_logs() {
    service=$1
    if [ -z "$service" ]; then
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f
    else
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" logs -f "$service"
    fi
}

# Function to show status
show_status() {
    print_color "$BLUE" "Service Status:"
    $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" ps
    
    echo ""
    print_color "$BLUE" "Redis Queue Status:"
    docker exec -it decentralized-sequencer_redis_1 redis-cli LLEN submissionQueue 2>/dev/null || echo "Queue not accessible"
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
        $DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" up -d sequencer-custom
        print_color "$GREEN" "✓ Snapshot sequencer launched with your custom settings"
        ;;
    distributed)
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
    *)
        show_usage
        exit 1
        ;;
esac