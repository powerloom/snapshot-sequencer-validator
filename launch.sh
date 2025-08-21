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
COMPOSE_FILE="docker-compose.unified.yml"
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
    echo "  unified       - Launch all-in-one unified sequencer"
    echo "  distributed   - Launch distributed components (listener, dequeuer, finalizer)"
    echo "  validators    - Launch 3 validator nodes for consensus"
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
    echo "  $0 unified                    # Run all-in-one sequencer"
    echo "  $0 distributed --debug        # Run distributed with debug"
    echo "  $0 validators                 # Run 3 validators"
    echo "  $0 custom listener,dequeuer   # Run specific components"
}

# Function to check prerequisites
check_prerequisites() {
    if ! command -v docker &> /dev/null; then
        print_color "$RED" "Error: Docker is not installed"
        exit 1
    fi
    
    if ! command -v docker-compose &> /dev/null; then
        print_color "$RED" "Error: Docker Compose is not installed"
        exit 1
    fi
    
    print_color "$GREEN" "✓ Prerequisites checked"
}

# Function to create default env file if not exists
create_env_file() {
    if [ ! -f "$ENV_FILE" ]; then
        print_color "$YELLOW" "Creating default .env file..."
        cat > "$ENV_FILE" << EOF
# Bootstrap node configuration
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/12D3KooWN4ovysY4dp45NhLPQ9ywEhK3Z1GmaCVhQKrKHrtk1R2x

# Debug mode
DEBUG_MODE=false

# Component scaling
DEQUEUER_REPLICAS=3
FINALIZER_REPLICAS=2

# Storage configuration
STORAGE_PROVIDER=ipfs
DA_PROVIDER=none
IPFS_HOST=ipfs:5001

# Ethereum RPC
RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY

# Private keys (generate unique keys for production)
PRIVATE_KEY_ALL=
PRIVATE_KEY_LISTENER=
PRIVATE_KEY_FINALIZER=
PRIVATE_KEY_VAL1=
PRIVATE_KEY_VAL2=
PRIVATE_KEY_VAL3=
EOF
        print_color "$GREEN" "✓ Created .env file"
    fi
}

# Function to launch unified mode
launch_unified() {
    print_color "$BLUE" "Launching unified sequencer (all-in-one)..."
    docker-compose -f "$COMPOSE_FILE" --profile unified up -d
    print_color "$GREEN" "✓ Unified sequencer launched"
}

# Function to launch distributed mode
launch_distributed() {
    print_color "$BLUE" "Launching distributed sequencer..."
    docker-compose -f "$COMPOSE_FILE" --profile distributed up -d
    print_color "$GREEN" "✓ Distributed sequencer launched"
    echo ""
    echo "Components running:"
    echo "  - Listener: Port 9100"
    echo "  - Dequeuer: 3 replicas"
    echo "  - Finalizer: 2 replicas"
}

# Function to launch validators
launch_validators() {
    print_color "$BLUE" "Launching validator nodes..."
    docker-compose -f "$COMPOSE_FILE" --profile validators up -d
    print_color "$GREEN" "✓ Validators launched"
    echo ""
    echo "Validators running:"
    echo "  - Validator 1: Port 9011"
    echo "  - Validator 2: Port 9012"
    echo "  - Validator 3: Port 9013"
}

# Function to launch minimal setup
launch_minimal() {
    print_color "$BLUE" "Launching minimal setup..."
    docker-compose -f "$COMPOSE_FILE" up -d redis unified-all
    print_color "$GREEN" "✓ Minimal setup launched"
}

# Function to launch full stack
launch_full() {
    print_color "$BLUE" "Launching full stack with monitoring..."
    docker-compose -f "$COMPOSE_FILE" --profile distributed --profile validators --profile storage --profile monitoring up -d
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
    
    docker-compose -f "$COMPOSE_FILE" $profile_args up -d
    print_color "$GREEN" "✓ Custom configuration launched"
}

# Function to stop all services
stop_services() {
    print_color "$YELLOW" "Stopping all services..."
    docker-compose -f "$COMPOSE_FILE" down
    print_color "$GREEN" "✓ Services stopped"
}

# Function to clean everything
clean_all() {
    print_color "$RED" "WARNING: This will remove all containers and volumes!"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        docker-compose -f "$COMPOSE_FILE" down -v
        print_color "$GREEN" "✓ Cleaned all containers and volumes"
    else
        print_color "$YELLOW" "Cancelled"
    fi
}

# Function to show logs
show_logs() {
    service=$1
    if [ -z "$service" ]; then
        docker-compose -f "$COMPOSE_FILE" logs -f
    else
        docker-compose -f "$COMPOSE_FILE" logs -f "$service"
    fi
}

# Function to show status
show_status() {
    print_color "$BLUE" "Service Status:"
    docker-compose -f "$COMPOSE_FILE" ps
    
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
create_env_file

# Load environment file
if [ -f "$ENV_FILE" ]; then
    export $(grep -v '^#' "$ENV_FILE" | xargs)
fi

case $COMMAND in
    unified)
        launch_unified
        ;;
    distributed)
        launch_distributed
        ;;
    validators)
        launch_validators
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