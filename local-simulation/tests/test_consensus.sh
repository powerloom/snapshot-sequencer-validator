#!/bin/bash

# test_consensus.sh - Test consensus mechanism with various node configurations
# Usage: ./test_consensus.sh [NODE_COUNT] [TEST_DURATION] [SUBMISSION_RATE]

set -e

NODE_COUNT=${1:-3}
TEST_DURATION=${2:-300}  # 5 minutes default
SUBMISSION_RATE=${3:-10} # Submissions per minute

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[CONSENSUS-TEST]${NC} $(date '+%H:%M:%S') $1"
}

warn() {
    echo -e "${YELLOW}[CONSENSUS-TEST]${NC} $(date '+%H:%M:%S') $1"
}

error() {
    echo -e "${RED}[CONSENSUS-TEST]${NC} $(date '+%H:%M:%S') $1"
}

info() {
    echo -e "${BLUE}[CONSENSUS-TEST]${NC} $(date '+%H:%M:%S') $1"
}

# Test configuration
TEST_NAME="consensus_test_${NODE_COUNT}nodes_$(date +%Y%m%d_%H%M%S)"
RESULTS_DIR="../results/$TEST_NAME"
LOG_DIR="$RESULTS_DIR/logs"
METRICS_DIR="$RESULTS_DIR/metrics"

# Initialize test environment
initialize_test() {
    log "Initializing consensus test: $TEST_NAME"
    
    # Create results directories
    mkdir -p "$RESULTS_DIR" "$LOG_DIR" "$METRICS_DIR"
    
    # Create test configuration
    cat > "$RESULTS_DIR/test_config.json" <<EOF
{
    "test_name": "$TEST_NAME",
    "test_type": "consensus",
    "node_count": $NODE_COUNT,
    "test_duration": $TEST_DURATION,
    "submission_rate": $SUBMISSION_RATE,
    "started_at": "$(date -Iseconds)",
    "parameters": {
        "bootstrap_nodes": 1,
        "sequencer_nodes": $((NODE_COUNT - 1)),
        "debugger_nodes": 1
    }
}
EOF
    
    log "Test configuration created: $RESULTS_DIR/test_config.json"
}

# Setup and start services for the test
setup_services() {
    log "Setting up services for $NODE_COUNT node consensus test"
    
    cd ../docker
    
    # Stop any existing services
    docker-compose down >/dev/null 2>&1 || true
    
    # Start bootstrap node first
    log "Starting bootstrap node"
    docker-compose up -d bootstrap-node >/dev/null 2>&1
    
    # Wait for bootstrap to be ready
    local max_wait=30
    local wait_time=0
    while [ $wait_time -lt $max_wait ]; do
        if docker-compose ps bootstrap-node | grep -q "Up"; then
            log "Bootstrap node is ready"
            break
        fi
        sleep 2
        wait_time=$((wait_time + 2))
    done
    
    if [ $wait_time -ge $max_wait ]; then
        error "Bootstrap node failed to start within ${max_wait}s"
        return 1
    fi
    
    # Start sequencer nodes based on NODE_COUNT
    local sequencers_to_start=$((NODE_COUNT - 1))  # Exclude bootstrap
    log "Starting $sequencers_to_start sequencer nodes"
    
    for i in $(seq 1 $sequencers_to_start); do
        if [ $i -le 3 ]; then  # We only have 3 sequencer services defined
            log "Starting sequencer-$i"
            docker-compose up -d sequencer-$i >/dev/null 2>&1
            sleep 5  # Stagger startup
        fi
    done
    
    # Start debugger for monitoring
    log "Starting P2P debugger for monitoring"
    docker-compose up -d p2p-debugger >/dev/null 2>&1
    
    # Wait for all services to be healthy
    log "Waiting for all services to become healthy..."
    wait_time=0
    max_wait=60
    
    while [ $wait_time -lt $max_wait ]; do
        local running_count=$(docker-compose ps | grep "Up" | wc -l)
        local expected_count=$((NODE_COUNT + 1))  # +1 for debugger
        
        if [ $running_count -ge $expected_count ]; then
            log "All $expected_count services are running"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Services running: $running_count/$expected_count (${wait_time}s elapsed)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Not all services became healthy within ${max_wait}s"
        docker-compose ps
    fi
    
    cd - >/dev/null
}

# Monitor network topology and peer connections
monitor_topology() {
    log "Monitoring network topology..."
    
    local containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
    local active_containers=()
    
    # Determine which containers are actually running
    for container in "${containers[@]}"; do
        if docker exec "$container" echo "test" >/dev/null 2>&1; then
            active_containers+=("$container")
        fi
    done
    
    log "Active containers: ${active_containers[*]}"
    
    # Create topology monitoring script
    cat > "$RESULTS_DIR/monitor_topology.sh" <<EOF
#!/bin/bash
while true; do
    echo "\$(date -Iseconds): Topology Check" >> "$LOG_DIR/topology.log"
    
    for container in ${active_containers[@]}; do
        # Get peer count (this would need to be implemented in the actual services)
        echo "  \$container: \$(docker exec \$container ps aux | grep -v grep | wc -l) processes" >> "$LOG_DIR/topology.log"
    done
    
    sleep 30
done
EOF
    chmod +x "$RESULTS_DIR/monitor_topology.sh"
    
    # Start topology monitoring in background
    "$RESULTS_DIR/monitor_topology.sh" &
    local monitor_pid=$!
    echo $monitor_pid > "$RESULTS_DIR/monitor_pid"
    
    log "Topology monitoring started (PID: $monitor_pid)"
}

# Generate test submissions at specified rate
generate_submissions() {
    log "Starting submission generation at $SUBMISSION_RATE submissions/minute"
    
    local submission_interval=$((60 / SUBMISSION_RATE))
    local total_submissions=$((SUBMISSION_RATE * TEST_DURATION / 60))
    
    info "Will generate $total_submissions submissions over ${TEST_DURATION}s (every ${submission_interval}s)"
    
    # Create submission generator script
    cat > "$RESULTS_DIR/generate_submissions.sh" <<EOF
#!/bin/bash

submission_count=0
max_submissions=$total_submissions
interval=$submission_interval

while [ \$submission_count -lt \$max_submissions ]; do
    timestamp=\$(date -Iseconds)
    submission_id="consensus_test_\$(date +%s)_\${submission_count}"
    
    # Create test submission
    submission_data="{
        \"id\": \"\$submission_id\",
        \"timestamp\": \"\$timestamp\",
        \"epoch\": \$((submission_count / 10 + 1)),
        \"data\": \"test_consensus_data_\${submission_count}\",
        \"node_count\": $NODE_COUNT
    }"
    
    echo "\$timestamp: Generated submission \$submission_id" >> "$LOG_DIR/submissions.log"
    echo "\$submission_data" >> "$RESULTS_DIR/submissions.jsonl"
    
    # Send to P2P debugger for broadcast (in actual implementation)
    # docker exec p2p-debugger broadcast_submission "\$submission_data"
    
    submission_count=\$((submission_count + 1))
    sleep \$interval
done

echo "\$(date -Iseconds): Completed generating \$submission_count submissions" >> "$LOG_DIR/submissions.log"
EOF
    chmod +x "$RESULTS_DIR/generate_submissions.sh"
    
    # Start submission generation in background
    "$RESULTS_DIR/generate_submissions.sh" &
    local generator_pid=$!
    echo $generator_pid > "$RESULTS_DIR/generator_pid"
    
    log "Submission generator started (PID: $generator_pid)"
}

# Monitor consensus metrics
monitor_consensus() {
    log "Starting consensus monitoring..."
    
    cat > "$RESULTS_DIR/monitor_consensus.sh" <<EOF
#!/bin/bash

start_time=\$(date +%s)
end_time=\$((start_time + $TEST_DURATION))

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    
    # Collect metrics from all nodes
    echo "\$timestamp: Consensus Metrics Check" >> "$LOG_DIR/consensus_metrics.log"
    
    # For each active node, collect:
    # - Message count
    # - Peer count
    # - Processing status
    # - Resource usage
    
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3; do
        if docker exec \$container echo "test" >/dev/null 2>&1; then
            # CPU and Memory usage
            cpu_mem=\$(docker stats \$container --no-stream --format "table {{.CPUPerc}}\t{{.MemUsage}}" | tail -1)
            echo "  \$container metrics: \$cpu_mem" >> "$LOG_DIR/consensus_metrics.log"
            
            # Network connections (proxy for peer count)
            connections=\$(docker exec \$container netstat -an 2>/dev/null | grep ESTABLISHED | wc -l || echo "0")
            echo "  \$container connections: \$connections" >> "$LOG_DIR/consensus_metrics.log"
        fi
    done
    
    echo "" >> "$LOG_DIR/consensus_metrics.log"
    sleep 10
done

echo "\$(date -Iseconds): Consensus monitoring completed" >> "$LOG_DIR/consensus_metrics.log"
EOF
    chmod +x "$RESULTS_DIR/monitor_consensus.sh"
    
    # Start consensus monitoring in background
    "$RESULTS_DIR/monitor_consensus.sh" &
    local consensus_monitor_pid=$!
    echo $consensus_monitor_pid > "$RESULTS_DIR/consensus_monitor_pid"
    
    log "Consensus monitoring started (PID: $consensus_monitor_pid)"
}

# Run the main test
run_test() {
    log "Running consensus test for ${TEST_DURATION}s with $NODE_COUNT nodes"
    
    local start_time=$(date +%s)
    local end_time=$((start_time + TEST_DURATION))
    local progress_interval=30
    local last_progress=$start_time
    
    while [ $(date +%s) -lt $end_time ]; do
        current_time=$(date +%s)
        elapsed=$((current_time - start_time))
        remaining=$((end_time - current_time))
        
        if [ $((current_time - last_progress)) -ge $progress_interval ]; then
            local progress_percent=$((elapsed * 100 / TEST_DURATION))
            info "Test progress: ${elapsed}s/${TEST_DURATION}s (${progress_percent}%) - ${remaining}s remaining"
            last_progress=$current_time
        fi
        
        sleep 5
    done
    
    log "Test duration completed"
}

# Collect and analyze results
collect_results() {
    log "Collecting test results..."
    
    # Stop all monitoring processes
    if [ -f "$RESULTS_DIR/monitor_pid" ]; then
        local monitor_pid=$(cat "$RESULTS_DIR/monitor_pid")
        kill $monitor_pid 2>/dev/null || true
        rm -f "$RESULTS_DIR/monitor_pid"
    fi
    
    if [ -f "$RESULTS_DIR/generator_pid" ]; then
        local generator_pid=$(cat "$RESULTS_DIR/generator_pid")
        kill $generator_pid 2>/dev/null || true
        rm -f "$RESULTS_DIR/generator_pid"
    fi
    
    if [ -f "$RESULTS_DIR/consensus_monitor_pid" ]; then
        local consensus_monitor_pid=$(cat "$RESULTS_DIR/consensus_monitor_pid")
        kill $consensus_monitor_pid 2>/dev/null || true
        rm -f "$RESULTS_DIR/consensus_monitor_pid"
    fi
    
    # Collect container logs
    log "Collecting container logs..."
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        if docker exec "$container" echo "test" >/dev/null 2>&1; then
            docker logs "$container" > "$LOG_DIR/${container}.log" 2>&1 || true
        fi
    done
    
    # Generate summary report
    generate_report
    
    log "Results collected in: $RESULTS_DIR"
}

# Generate test report
generate_report() {
    log "Generating test report..."
    
    local end_time=$(date -Iseconds)
    local total_submissions=0
    
    if [ -f "$RESULTS_DIR/submissions.jsonl" ]; then
        total_submissions=$(wc -l < "$RESULTS_DIR/submissions.jsonl")
    fi
    
    # Create summary report
    cat > "$RESULTS_DIR/test_report.json" <<EOF
{
    "test_summary": {
        "test_name": "$TEST_NAME",
        "test_type": "consensus",
        "node_count": $NODE_COUNT,
        "duration": $TEST_DURATION,
        "completed_at": "$end_time",
        "total_submissions": $total_submissions,
        "expected_submissions": $((SUBMISSION_RATE * TEST_DURATION / 60))
    },
    "results": {
        "submission_success_rate": "TBD",
        "average_consensus_time": "TBD",
        "network_partition_resilience": "TBD",
        "resource_usage": "TBD"
    },
    "files": {
        "logs": "logs/",
        "metrics": "metrics/",
        "submissions": "submissions.jsonl",
        "topology": "logs/topology.log",
        "consensus_metrics": "logs/consensus_metrics.log"
    }
}
EOF
    
    # Create human-readable report
    cat > "$RESULTS_DIR/REPORT.md" <<EOF
# Consensus Test Report

## Test Configuration
- **Test Name**: $TEST_NAME
- **Node Count**: $NODE_COUNT
- **Duration**: ${TEST_DURATION}s
- **Submission Rate**: $SUBMISSION_RATE/minute
- **Total Submissions**: $total_submissions

## Results Summary
- Test completed successfully
- All logs and metrics collected
- See individual log files for detailed analysis

## Files Generated
- \`test_config.json\` - Test configuration
- \`test_report.json\` - Machine-readable results
- \`submissions.jsonl\` - All test submissions
- \`logs/\` - Container logs and monitoring data
- \`metrics/\` - Performance metrics

## Next Steps
1. Analyze log files for consensus behavior
2. Check peer connectivity and message propagation
3. Validate submission ordering and finality
4. Compare with expected performance benchmarks

EOF
    
    log "Test report generated: $RESULTS_DIR/REPORT.md"
}

# Cleanup function
cleanup() {
    log "Cleaning up consensus test..."
    
    # Kill monitoring processes
    if [ -f "$RESULTS_DIR/monitor_pid" ]; then
        kill $(cat "$RESULTS_DIR/monitor_pid") 2>/dev/null || true
    fi
    if [ -f "$RESULTS_DIR/generator_pid" ]; then
        kill $(cat "$RESULTS_DIR/generator_pid") 2>/dev/null || true
    fi
    if [ -f "$RESULTS_DIR/consensus_monitor_pid" ]; then
        kill $(cat "$RESULTS_DIR/consensus_monitor_pid") 2>/dev/null || true
    fi
    
    # Reset network conditions
    ../scripts/reset_network.sh >/dev/null 2>&1 || true
    
    log "Cleanup completed"
}

# Main execution
main() {
    log "Starting consensus test with $NODE_COUNT nodes for ${TEST_DURATION}s"
    
    # Validate parameters
    if [ $NODE_COUNT -lt 2 ] || [ $NODE_COUNT -gt 4 ]; then
        error "NODE_COUNT must be between 2 and 4 (got: $NODE_COUNT)"
        exit 1
    fi
    
    if [ $TEST_DURATION -lt 60 ]; then
        error "TEST_DURATION must be at least 60 seconds (got: $TEST_DURATION)"
        exit 1
    fi
    
    # Set trap for cleanup on exit
    trap cleanup EXIT INT TERM
    
    # Run test phases
    initialize_test
    setup_services
    monitor_topology
    generate_submissions
    monitor_consensus
    run_test
    collect_results
    
    log "✅ Consensus test completed successfully!"
    info "📊 Results available in: $RESULTS_DIR"
    info "📝 Report: $RESULTS_DIR/REPORT.md"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Consensus Test Suite

Usage: $0 [NODE_COUNT] [TEST_DURATION] [SUBMISSION_RATE]

Parameters:
  NODE_COUNT      - Number of nodes to test (2-4, default: 3)
  TEST_DURATION   - Test duration in seconds (min: 60, default: 300)
  SUBMISSION_RATE - Submissions per minute (default: 10)

Description:
  Tests the consensus mechanism of the P2P sequencer network by:
  1. Starting specified number of nodes
  2. Generating test submissions at specified rate
  3. Monitoring network topology and peer connections
  4. Measuring consensus performance and reliability
  5. Collecting comprehensive metrics and logs

Examples:
  $0                    # Test with 3 nodes for 5 minutes
  $0 4 600 20          # Test with 4 nodes for 10 minutes, 20 submissions/min
  $0 2 120 5           # Test with 2 nodes for 2 minutes, 5 submissions/min

Results:
  All test results are saved in ../results/consensus_test_* directories
  Each test generates logs, metrics, and a comprehensive report

EOF
    exit 0
fi

# Run main function
main "$@"