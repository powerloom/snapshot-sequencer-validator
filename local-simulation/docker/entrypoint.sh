#!/bin/bash

set -e

# Function to wait for bootstrap node
wait_for_bootstrap() {
    if [ "$SERVICE_TYPE" != "bootstrap" ] && [ ! -z "$BOOTSTRAP_PEERS" ]; then
        echo "Waiting for bootstrap node to be ready..."
        local bootstrap_ip=$(echo "$BOOTSTRAP_PEERS" | grep -oP '(?<=/ip4/)[^/]+')
        local bootstrap_port=$(echo "$BOOTSTRAP_PEERS" | grep -oP '(?<=/tcp/)[^/]+')
        
        while ! nc -z "$bootstrap_ip" "$bootstrap_port"; do
            echo "Bootstrap node not ready, waiting..."
            sleep 2
        done
        echo "Bootstrap node is ready!"
    fi
}

# Function to setup network simulation capabilities
setup_network() {
    echo "Setting up network simulation capabilities..."
    
    # Enable IP forwarding for network simulation
    echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true
    
    # Set up traffic control if available
    tc qdisc add dev eth0 root handle 1: htb 2>/dev/null || echo "TC not available, skipping"
    
    echo "Network setup complete"
}

# Function to setup logging
setup_logging() {
    local log_dir="/app/logs"
    mkdir -p "$log_dir"
    
    # Create log files
    touch "$log_dir/${NODE_ID}.log"
    touch "$log_dir/${NODE_ID}-error.log"
    
    echo "Logging setup complete for node: $NODE_ID"
}

# Function to generate config files
generate_config() {
    local config_dir="/app/configs"
    mkdir -p "$config_dir"
    
    # Generate node-specific config
    cat > "$config_dir/${NODE_ID}-config.json" <<EOF
{
    "node_id": "$NODE_ID",
    "service_type": "$SERVICE_TYPE",
    "p2p_port": $P2P_PORT,
    "api_port": ${API_PORT:-8081},
    "bootstrap_peers": "$BOOTSTRAP_PEERS",
    "rendezvous_string": "$RENDEZVOUS_STRING",
    "discovery_topic": "$DISCOVERY_TOPIC",
    "submission_topic": "$SUBMISSION_TOPIC",
    "log_level": "$LOG_LEVEL",
    "private_key": "$PRIVATE_KEY",
    "started_at": "$(date -Iseconds)"
}
EOF
    
    echo "Configuration generated for $NODE_ID"
}

# Function to start bootstrap service
start_bootstrap() {
    echo "Starting Bootstrap Node..."
    exec ./bootstrap-service 2>&1 | tee "/app/logs/${NODE_ID}.log"
}

# Function to start sequencer service
start_sequencer() {
    echo "Starting Sequencer Node: $NODE_ID"
    
    # Set environment variables for the sequencer
    export LISTEN_PORT="$P2P_PORT"
    export API_PORT="${API_PORT:-8081}"
    
    exec ./sequencer-service 2>&1 | tee "/app/logs/${NODE_ID}.log"
}

# Function to start debugger service
start_debugger() {
    echo "Starting P2P Debugger in mode: ${DEBUGGER_MODE:-LISTENER}"
    
    # Set debugger-specific environment
    export MODE="${DEBUGGER_MODE:-LISTENER}"
    export LISTEN_PORT="$P2P_PORT"
    
    exec ./debugger-service 2>&1 | tee "/app/logs/${NODE_ID}.log"
}

# Function to start network simulator (utility container)
start_network_simulator() {
    echo "Starting Network Simulator container..."
    
    # Install additional tools
    apk add --no-cache docker-cli 2>/dev/null || true
    
    # Keep container running and provide utilities
    echo "Network simulator ready. Available commands:"
    echo "  - /scripts/simulate_latency.sh"
    echo "  - /scripts/simulate_packet_loss.sh"
    echo "  - /scripts/simulate_partition.sh"
    echo "  - /scripts/reset_network.sh"
    
    tail -f /dev/null
}

# Main execution
main() {
    echo "Starting container for service: $SERVICE_TYPE with node ID: $NODE_ID"
    
    # Setup common components
    setup_logging
    generate_config
    setup_network
    
    # Wait for dependencies
    wait_for_bootstrap
    
    # Start appropriate service
    case "$SERVICE_TYPE" in
        "bootstrap")
            start_bootstrap
            ;;
        "sequencer")
            start_sequencer
            ;;
        "debugger")
            start_debugger
            ;;
        "network-simulator")
            start_network_simulator
            ;;
        *)
            echo "Unknown service type: $SERVICE_TYPE"
            echo "Supported types: bootstrap, sequencer, debugger, network-simulator"
            exit 1
            ;;
    esac
}

# Handle shutdown gracefully
shutdown() {
    echo "Shutting down $SERVICE_TYPE service..."
    # Kill all child processes
    pkill -P $$ || true
    exit 0
}

trap shutdown SIGTERM SIGINT

# Run main function
main "$@"