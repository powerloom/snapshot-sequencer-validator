#!/bin/bash

# test_byzantine.sh - Test Byzantine fault tolerance
# Usage: ./test_byzantine.sh [BYZANTINE_NODES] [FAULT_TYPE] [TEST_DURATION]

set -e

BYZANTINE_NODES=${1:-1}
FAULT_TYPE=${2:-"delay"}  # Options: delay, partition, corrupt, crash
TEST_DURATION=${3:-300}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[BYZANTINE-TEST]${NC} $(date '+%H:%M:%S') $1"
}

warn() {
    echo -e "${YELLOW}[BYZANTINE-TEST]${NC} $(date '+%H:%M:%S') $1"
}

error() {
    echo -e "${RED}[BYZANTINE-TEST]${NC} $(date '+%H:%M:%S') $1"
}

info() {
    echo -e "${BLUE}[BYZANTINE-TEST]${NC} $(date '+%H:%M:%S') $1"
}

byzantine() {
    echo -e "${PURPLE}[BYZANTINE-TEST]${NC} $(date '+%H:%M:%S') 🔥 $1"
}

# Test configuration
TEST_NAME="byzantine_test_${BYZANTINE_NODES}nodes_${FAULT_TYPE}_$(date +%Y%m%d_%H%M%S)"
RESULTS_DIR="../results/$TEST_NAME"
LOG_DIR="$RESULTS_DIR/logs"
METRICS_DIR="$RESULTS_DIR/metrics"

# Available nodes for Byzantine behavior
AVAILABLE_NODES=("p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3")

# Initialize test environment
initialize_test() {
    log "Initializing Byzantine fault tolerance test: $TEST_NAME"
    
    # Create results directories
    mkdir -p "$RESULTS_DIR" "$LOG_DIR" "$METRICS_DIR"
    
    # Select which nodes will exhibit Byzantine behavior
    local selected_nodes=()
    for ((i=0; i<BYZANTINE_NODES && i<${#AVAILABLE_NODES[@]}; i++)); do
        selected_nodes+=("${AVAILABLE_NODES[$i]}")
    done
    
    # Create test configuration
    cat > "$RESULTS_DIR/test_config.json" <<EOF
{
    "test_name": "$TEST_NAME",
    "test_type": "byzantine_fault_tolerance",
    "byzantine_node_count": $BYZANTINE_NODES,
    "fault_type": "$FAULT_TYPE",
    "test_duration": $TEST_DURATION,
    "started_at": "$(date -Iseconds)",
    "byzantine_nodes": $(printf '%s\n' "${selected_nodes[@]}" | jq -R . | jq -s .),
    "honest_nodes": $(printf '%s\n' p2p-bootstrap p2p-debugger | jq -R . | jq -s .),
    "network_topology": {
        "total_nodes": 5,
        "byzantine_nodes": $BYZANTINE_NODES,
        "honest_nodes": $((5 - BYZANTINE_NODES)),
        "fault_tolerance_threshold": "2/3 honest majority required"
    }
}
EOF
    
    log "Test configuration created for ${BYZANTINE_NODES} Byzantine nodes"
    byzantine "Selected Byzantine nodes: ${selected_nodes[*]}"
    info "Fault type: $FAULT_TYPE"
}

# Setup and start all services
setup_services() {
    log "Setting up all services for Byzantine test"
    
    cd ../docker
    
    # Stop any existing services
    docker-compose down >/dev/null 2>&1 || true
    
    # Start all services
    log "Starting all services..."
    docker-compose up -d >/dev/null 2>&1
    
    # Wait for services to be healthy
    log "Waiting for services to become healthy..."
    local max_wait=60
    local wait_time=0
    
    while [ $wait_time -lt $max_wait ]; do
        local healthy_count=$(docker-compose ps | grep "Up" | wc -l)
        if [ $healthy_count -ge 5 ]; then
            log "All services are running and healthy"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Services health check: $healthy_count/5 (${wait_time}s elapsed)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Not all services became healthy within ${max_wait}s"
        docker-compose ps
    fi
    
    cd - >/dev/null
    
    # Allow network to stabilize
    log "Allowing network to stabilize for 30 seconds..."
    sleep 30
}

# Apply Byzantine behavior based on fault type
apply_byzantine_behavior() {
    local fault_type=$1
    shift
    local byzantine_nodes=("$@")
    
    byzantine "Applying '$fault_type' Byzantine behavior to: ${byzantine_nodes[*]}"
    
    case "$fault_type" in
        "delay")
            apply_delay_fault "${byzantine_nodes[@]}"
            ;;
        "partition")
            apply_partition_fault "${byzantine_nodes[@]}"
            ;;
        "corrupt")
            apply_corruption_fault "${byzantine_nodes[@]}"
            ;;
        "crash")
            apply_crash_fault "${byzantine_nodes[@]}"
            ;;
        "intermittent")
            apply_intermittent_fault "${byzantine_nodes[@]}"
            ;;
        *)
            error "Unknown fault type: $fault_type"
            exit 1
            ;;
    esac
}

# Byzantine fault: Excessive delays
apply_delay_fault() {
    local nodes=("$@")
    byzantine "Applying excessive delay fault to: ${nodes[*]}"
    
    for node in "${nodes[@]}"; do
        log "Adding 2000ms delay to $node"
        docker exec -it "$node" tc qdisc add dev eth0 root netem delay 2000ms 500ms 2>/dev/null || {
            docker exec -it "$node" tc qdisc replace dev eth0 root netem delay 2000ms 500ms 2>/dev/null || {
                warn "Failed to apply delay to $node"
            }
        }
        
        # Log the fault application
        echo "$(date -Iseconds): Applied 2000ms±500ms delay to $node" >> "$LOG_DIR/byzantine_faults.log"
    done
}

# Byzantine fault: Network partitioning
apply_partition_fault() {
    local nodes=("$@")
    byzantine "Applying partition fault to: ${nodes[*]}"
    
    # Partition Byzantine nodes from honest nodes
    local honest_nodes=("p2p-bootstrap" "p2p-debugger")
    
    # Add remaining sequencers to honest nodes
    for sequencer in "${AVAILABLE_NODES[@]}"; do
        local is_byzantine=false
        for byzantine_node in "${nodes[@]}"; do
            if [ "$sequencer" = "$byzantine_node" ]; then
                is_byzantine=true
                break
            fi
        done
        
        if [ "$is_byzantine" = false ]; then
            honest_nodes+=("$sequencer")
        fi
    done
    
    log "Honest nodes: ${honest_nodes[*]}"
    
    # Block communication between Byzantine and honest nodes
    for byzantine_node in "${nodes[@]}"; do
        for honest_node in "${honest_nodes[@]}"; do
            local honest_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$honest_node" 2>/dev/null)
            if [ ! -z "$honest_ip" ]; then
                docker exec -it "$byzantine_node" iptables -A OUTPUT -d "$honest_ip" -j DROP 2>/dev/null || true
                log "Blocked $byzantine_node -> $honest_node ($honest_ip)"
            fi
            
            local byzantine_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$byzantine_node" 2>/dev/null)
            if [ ! -z "$byzantine_ip" ]; then
                docker exec -it "$honest_node" iptables -A OUTPUT -d "$byzantine_ip" -j DROP 2>/dev/null || true
                log "Blocked $honest_node -> $byzantine_node ($byzantine_ip)"
            fi
        done
        echo "$(date -Iseconds): Partitioned $byzantine_node from honest nodes" >> "$LOG_DIR/byzantine_faults.log"
    done
}

# Byzantine fault: Message corruption
apply_corruption_fault() {
    local nodes=("$@")
    byzantine "Applying message corruption fault to: ${nodes[*]}"
    
    for node in "${nodes[@]}"; do
        # Simulate message corruption with high packet loss and duplication
        docker exec -it "$node" tc qdisc add dev eth0 root netem loss 10% duplicate 5% corrupt 5% 2>/dev/null || {
            docker exec -it "$node" tc qdisc replace dev eth0 root netem loss 10% duplicate 5% corrupt 5% 2>/dev/null || {
                warn "Failed to apply corruption to $node"
            }
        }
        
        log "Applied message corruption (10% loss, 5% duplicate, 5% corrupt) to $node"
        echo "$(date -Iseconds): Applied message corruption to $node" >> "$LOG_DIR/byzantine_faults.log"
    done
}

# Byzantine fault: Node crashes
apply_crash_fault() {
    local nodes=("$@")
    byzantine "Applying crash fault to: ${nodes[*]}"
    
    for node in "${nodes[@]}"; do
        log "Stopping $node (simulating crash)"
        docker-compose -f ../docker/docker-compose.yml stop "$node" >/dev/null 2>&1 || {
            warn "Failed to stop $node"
        }
        
        echo "$(date -Iseconds): Crashed $node" >> "$LOG_DIR/byzantine_faults.log"
    done
    
    # Schedule recovery after half the test duration
    local recovery_time=$((TEST_DURATION / 2))
    byzantine "Scheduling recovery of crashed nodes in ${recovery_time}s"
    
    (
        sleep $recovery_time
        byzantine "Recovering crashed nodes: ${nodes[*]}"
        for node in "${nodes[@]}"; do
            docker-compose -f ../docker/docker-compose.yml start "$node" >/dev/null 2>&1
            echo "$(date -Iseconds): Recovered $node" >> "$LOG_DIR/byzantine_faults.log"
        done
    ) &
    
    echo $! > "$RESULTS_DIR/recovery_pid"
}

# Byzantine fault: Intermittent failures
apply_intermittent_fault() {
    local nodes=("$@")
    byzantine "Applying intermittent fault to: ${nodes[*]}"
    
    # Create intermittent fault script
    cat > "$RESULTS_DIR/intermittent_faults.sh" <<EOF
#!/bin/bash

nodes=(${nodes[@]})
end_time=\$(($(date +%s) + $TEST_DURATION))

while [ \$(date +%s) -lt \$end_time ]; do
    # Random fault duration (30-120 seconds)
    fault_duration=\$((30 + RANDOM % 90))
    
    # Random recovery period (60-180 seconds)
    recovery_period=\$((60 + RANDOM % 120))
    
    for node in "\${nodes[@]}"; do
        # Apply random fault type
        fault_types=("delay" "packet_loss" "brief_partition")
        fault_type=\${fault_types[\$((RANDOM % \${#fault_types[@]}))]}
        
        echo "\$(date -Iseconds): Applying \$fault_type to \$node for \${fault_duration}s" >> "$LOG_DIR/byzantine_faults.log"
        
        case "\$fault_type" in
            "delay")
                docker exec -it "\$node" tc qdisc replace dev eth0 root netem delay \$((500 + RANDOM % 1500))ms 2>/dev/null || true
                ;;
            "packet_loss")
                docker exec -it "\$node" tc qdisc replace dev eth0 root netem loss \$((5 + RANDOM % 15))% 2>/dev/null || true
                ;;
            "brief_partition")
                # Briefly isolate from one random peer
                other_node="\${AVAILABLE_NODES[\$((RANDOM % \${#AVAILABLE_NODES[@]}))]}"
                if [ "\$node" != "\$other_node" ]; then
                    other_ip=\$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "\$other_node" 2>/dev/null)
                    [ ! -z "\$other_ip" ] && docker exec -it "\$node" iptables -A OUTPUT -d "\$other_ip" -j DROP 2>/dev/null || true
                fi
                ;;
        esac
    done
    
    sleep \$fault_duration
    
    # Recovery phase
    echo "\$(date -Iseconds): Starting recovery phase for \${recovery_period}s" >> "$LOG_DIR/byzantine_faults.log"
    
    for node in "\${nodes[@]}"; do
        # Reset network conditions
        docker exec -it "\$node" tc qdisc del dev eth0 root 2>/dev/null || true
        docker exec -it "\$node" iptables -F OUTPUT 2>/dev/null || true
    done
    
    sleep \$recovery_period
done

echo "\$(date -Iseconds): Intermittent fault simulation completed" >> "$LOG_DIR/byzantine_faults.log"
EOF
    
    chmod +x "$RESULTS_DIR/intermittent_faults.sh"
    
    # Start intermittent faults in background
    "$RESULTS_DIR/intermittent_faults.sh" &
    local intermittent_pid=$!
    echo $intermittent_pid > "$RESULTS_DIR/intermittent_pid"
    
    log "Intermittent fault simulation started (PID: $intermittent_pid)"
}

# Generate Byzantine attack submissions
generate_byzantine_submissions() {
    byzantine "Starting Byzantine submission generation"
    
    cat > "$RESULTS_DIR/byzantine_generator.sh" <<EOF
#!/bin/bash

submission_count=0
max_submissions=\$((10 * $TEST_DURATION / 60))  # 10 per minute
interval=6  # Every 6 seconds

while [ \$submission_count -lt \$max_submissions ]; do
    timestamp=\$(date -Iseconds)
    submission_id="byzantine_attack_\$(date +%s)_\${submission_count}"
    
    # Create malformed submission (Byzantine behavior)
    submission_data="{
        \"id\": \"\$submission_id\",
        \"timestamp\": \"\$timestamp\",
        \"epoch\": -1,
        \"data\": \"BYZANTINE_ATTACK_DATA_\${submission_count}\",
        \"signature\": \"INVALID_SIGNATURE\",
        \"byzantine_flag\": true
    }"
    
    echo "\$timestamp: Generated Byzantine submission \$submission_id" >> "$LOG_DIR/byzantine_submissions.log"
    echo "\$submission_data" >> "$RESULTS_DIR/byzantine_submissions.jsonl"
    
    submission_count=\$((submission_count + 1))
    sleep \$interval
done
EOF
    
    chmod +x "$RESULTS_DIR/byzantine_generator.sh"
    
    # Start Byzantine submission generation
    "$RESULTS_DIR/byzantine_generator.sh" &
    local byzantine_gen_pid=$!
    echo $byzantine_gen_pid > "$RESULTS_DIR/byzantine_gen_pid"
    
    byzantine "Byzantine submission generator started (PID: $byzantine_gen_pid)"
}

# Monitor Byzantine behavior and system response
monitor_byzantine_behavior() {
    log "Starting Byzantine behavior monitoring..."
    
    cat > "$RESULTS_DIR/monitor_byzantine.sh" <<EOF
#!/bin/bash

start_time=\$(date +%s)
end_time=\$((start_time + $TEST_DURATION))

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    
    echo "\$timestamp: Byzantine Behavior Check" >> "$LOG_DIR/byzantine_monitoring.log"
    
    # Monitor all nodes
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        if docker exec \$container echo "test" >/dev/null 2>&1; then
            # Resource usage
            resource_usage=\$(docker stats \$container --no-stream --format "{{.CPUPerc}} {{.MemUsage}}" 2>/dev/null || echo "N/A N/A")
            
            # Network connections
            connections=\$(docker exec \$container netstat -an 2>/dev/null | grep ESTABLISHED | wc -l || echo "0")
            
            # Traffic control status
            tc_status=\$(docker exec \$container tc qdisc show dev eth0 2>/dev/null | head -1 || echo "default")
            
            # IPTables rules count
            iptables_rules=\$(docker exec \$container iptables -L OUTPUT 2>/dev/null | wc -l || echo "0")
            
            echo "  \$container: CPU/Mem=\$resource_usage Conn=\$connections TC='\$tc_status' IPT=\$iptables_rules" >> "$LOG_DIR/byzantine_monitoring.log"
        else
            echo "  \$container: OFFLINE" >> "$LOG_DIR/byzantine_monitoring.log"
        fi
    done
    
    echo "" >> "$LOG_DIR/byzantine_monitoring.log"
    sleep 15
done
EOF
    
    chmod +x "$RESULTS_DIR/monitor_byzantine.sh"
    
    # Start monitoring in background
    "$RESULTS_DIR/monitor_byzantine.sh" &
    local monitor_pid=$!
    echo $monitor_pid > "$RESULTS_DIR/monitor_pid"
    
    log "Byzantine monitoring started (PID: $monitor_pid)"
}

# Run the main test
run_test() {
    log "Running Byzantine fault tolerance test for ${TEST_DURATION}s"
    byzantine "Fault type: $FAULT_TYPE affecting $BYZANTINE_NODES nodes"
    
    local start_time=$(date +%s)
    local end_time=$((start_time + TEST_DURATION))
    local progress_interval=30
    local last_progress=$start_time
    
    # Apply Byzantine faults
    local byzantine_nodes=()
    for ((i=0; i<BYZANTINE_NODES && i<${#AVAILABLE_NODES[@]}; i++)); do
        byzantine_nodes+=("${AVAILABLE_NODES[$i]}")
    done
    
    # Wait a bit before applying faults to establish baseline
    log "Establishing baseline for 60 seconds before applying faults..."
    sleep 60
    
    apply_byzantine_behavior "$FAULT_TYPE" "${byzantine_nodes[@]}"
    
    while [ $(date +%s) -lt $end_time ]; do
        current_time=$(date +%s)
        elapsed=$((current_time - start_time))
        remaining=$((end_time - current_time))
        
        if [ $((current_time - last_progress)) -ge $progress_interval ]; then
            local progress_percent=$((elapsed * 100 / TEST_DURATION))
            info "Byzantine test progress: ${elapsed}s/${TEST_DURATION}s (${progress_percent}%) - ${remaining}s remaining"
            
            # Show current Byzantine node status
            byzantine "Byzantine nodes status check:"
            for node in "${byzantine_nodes[@]}"; do
                local status="UNKNOWN"
                if docker exec "$node" echo "test" >/dev/null 2>&1; then
                    status="RUNNING"
                else
                    status="OFFLINE"
                fi
                byzantine "  $node: $status"
            done
            
            last_progress=$current_time
        fi
        
        sleep 5
    done
    
    log "Byzantine test duration completed"
}

# Collect results and generate report
collect_results() {
    log "Collecting Byzantine test results..."
    
    # Stop all monitoring processes
    for pid_file in monitor_pid byzantine_gen_pid intermittent_pid recovery_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            local pid=$(cat "$RESULTS_DIR/$pid_file")
            kill $pid 2>/dev/null || true
            rm -f "$RESULTS_DIR/$pid_file"
        fi
    done
    
    # Reset network conditions
    log "Resetting network conditions..."
    ../scripts/reset_network.sh >/dev/null 2>&1 || true
    
    # Collect container logs
    log "Collecting container logs..."
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        docker logs "$container" > "$LOG_DIR/${container}.log" 2>&1 || true
    done
    
    # Generate comprehensive report
    generate_byzantine_report
    
    log "Results collected in: $RESULTS_DIR"
}

# Generate Byzantine test report
generate_byzantine_report() {
    log "Generating Byzantine test report..."
    
    local end_time=$(date -Iseconds)
    local total_byzantine_submissions=0
    
    if [ -f "$RESULTS_DIR/byzantine_submissions.jsonl" ]; then
        total_byzantine_submissions=$(wc -l < "$RESULTS_DIR/byzantine_submissions.jsonl")
    fi
    
    # Create detailed report
    cat > "$RESULTS_DIR/test_report.json" <<EOF
{
    "test_summary": {
        "test_name": "$TEST_NAME",
        "test_type": "byzantine_fault_tolerance",
        "byzantine_node_count": $BYZANTINE_NODES,
        "fault_type": "$FAULT_TYPE",
        "duration": $TEST_DURATION,
        "completed_at": "$end_time",
        "total_byzantine_submissions": $total_byzantine_submissions
    },
    "byzantine_analysis": {
        "fault_tolerance_threshold": "2/3 honest majority",
        "network_resilience": "TBD - analyze logs",
        "consensus_integrity": "TBD - verify submission ordering",
        "recovery_time": "TBD - measure fault recovery",
        "performance_impact": "TBD - compare with baseline"
    },
    "fault_details": {
        "fault_type": "$FAULT_TYPE",
        "affected_nodes": $(jq -n --argjson nodes "$(printf '%s\n' "${AVAILABLE_NODES[@]:0:$BYZANTINE_NODES}" | jq -R . | jq -s .)" '$nodes'),
        "fault_patterns": "See byzantine_faults.log for detailed timeline"
    }
}
EOF
    
    # Create comprehensive human-readable report
    cat > "$RESULTS_DIR/BYZANTINE_REPORT.md" <<EOF
# Byzantine Fault Tolerance Test Report

## Test Summary
- **Test Name**: $TEST_NAME
- **Byzantine Nodes**: $BYZANTINE_NODES out of ${#AVAILABLE_NODES[@]} sequencers
- **Fault Type**: $FAULT_TYPE
- **Duration**: ${TEST_DURATION}s
- **Test Date**: $(date)

## Byzantine Fault Configuration
- **Affected Nodes**: ${AVAILABLE_NODES[@]:0:$BYZANTINE_NODES}
- **Fault Type Details**:
$(case "$FAULT_TYPE" in
    "delay") echo "  - Excessive network delays (2000ms±500ms)";;
    "partition") echo "  - Network partitioning from honest nodes";;
    "corrupt") echo "  - Message corruption (10% loss, 5% duplicate, 5% corrupt)";;
    "crash") echo "  - Node crashes with recovery after 50% test duration";;
    "intermittent") echo "  - Random intermittent faults every 60-180 seconds";;
esac)

## Expected Behavior
- **Honest Majority**: $((${#AVAILABLE_NODES[@]} - BYZANTINE_NODES + 2)) out of $((${#AVAILABLE_NODES[@]} + 2)) total nodes
- **Fault Tolerance**: System should continue operating with Byzantine nodes
- **Consensus**: Valid submissions should be processed despite Byzantine behavior
- **Recovery**: Network should recover when faults are resolved

## Analysis Required
1. **Log Analysis**: Review container logs for error patterns and recovery
2. **Consensus Verification**: Ensure valid submissions were processed correctly
3. **Performance Impact**: Measure latency and throughput degradation
4. **Network Resilience**: Verify honest nodes maintained connectivity

## Key Files
- \`logs/byzantine_faults.log\` - Timeline of Byzantine fault applications
- \`logs/byzantine_monitoring.log\` - System behavior during faults
- \`byzantine_submissions.jsonl\` - Malicious submissions generated
- \`logs/\` - Individual container logs for detailed analysis

## Recommendations
1. Analyze log patterns to identify Byzantine detection mechanisms
2. Verify consensus was not disrupted by Byzantine behavior
3. Measure recovery time after fault resolution
4. Compare performance metrics with baseline consensus test

EOF
    
    byzantine "Byzantine test report generated: $RESULTS_DIR/BYZANTINE_REPORT.md"
}

# Cleanup function
cleanup() {
    log "Cleaning up Byzantine test..."
    
    # Kill all monitoring processes
    for pid_file in monitor_pid byzantine_gen_pid intermittent_pid recovery_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            kill $(cat "$RESULTS_DIR/$pid_file") 2>/dev/null || true
        fi
    done
    
    # Reset network conditions
    ../scripts/reset_network.sh >/dev/null 2>&1 || true
    
    log "Byzantine test cleanup completed"
}

# Main execution
main() {
    byzantine "🔥 Starting Byzantine Fault Tolerance Test 🔥"
    log "Configuration: $BYZANTINE_NODES Byzantine nodes, $FAULT_TYPE faults, ${TEST_DURATION}s duration"
    
    # Validate parameters
    if [ $BYZANTINE_NODES -lt 1 ] || [ $BYZANTINE_NODES -gt ${#AVAILABLE_NODES[@]} ]; then
        error "BYZANTINE_NODES must be between 1 and ${#AVAILABLE_NODES[@]} (got: $BYZANTINE_NODES)"
        exit 1
    fi
    
    if [ $TEST_DURATION -lt 120 ]; then
        error "TEST_DURATION must be at least 120 seconds for Byzantine tests (got: $TEST_DURATION)"
        exit 1
    fi
    
    # Check if we can maintain honest majority
    local total_nodes=$((${#AVAILABLE_NODES[@]} + 2))  # +2 for bootstrap and debugger
    local honest_nodes=$((total_nodes - BYZANTINE_NODES))
    local required_honest=$((total_nodes * 2 / 3 + 1))
    
    if [ $honest_nodes -lt $required_honest ]; then
        warn "⚠️  Byzantine nodes ($BYZANTINE_NODES) may exceed fault tolerance threshold"
        warn "   Total nodes: $total_nodes, Honest: $honest_nodes, Required: $required_honest"
        warn "   This test will verify system behavior beyond fault tolerance limits"
    else
        log "✅ Honest majority maintained: $honest_nodes/$total_nodes honest nodes"
    fi
    
    # Set trap for cleanup on exit
    trap cleanup EXIT INT TERM
    
    # Run test phases
    initialize_test
    setup_services
    generate_byzantine_submissions
    monitor_byzantine_behavior
    run_test
    collect_results
    
    byzantine "🎯 Byzantine fault tolerance test completed!"
    info "📊 Results available in: $RESULTS_DIR"
    info "📝 Report: $RESULTS_DIR/BYZANTINE_REPORT.md"
    warn "🔍 Manual analysis required - review logs for Byzantine behavior detection"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Byzantine Fault Tolerance Test Suite

Usage: $0 [BYZANTINE_NODES] [FAULT_TYPE] [TEST_DURATION]

Parameters:
  BYZANTINE_NODES - Number of nodes to exhibit Byzantine behavior (1-3, default: 1)
  FAULT_TYPE      - Type of Byzantine fault (default: delay)
                    Options: delay, partition, corrupt, crash, intermittent
  TEST_DURATION   - Test duration in seconds (min: 120, default: 300)

Fault Types:
  delay        - Apply excessive network delays (2000ms±500ms)
  partition    - Partition Byzantine nodes from honest nodes
  corrupt      - Corrupt messages (loss, duplication, corruption)
  crash        - Crash nodes with recovery after 50% test duration
  intermittent - Random intermittent faults with recovery periods

Description:
  Tests Byzantine fault tolerance by:
  1. Starting all network services
  2. Establishing baseline behavior
  3. Applying specified Byzantine faults
  4. Generating malicious submissions
  5. Monitoring system response and recovery
  6. Analyzing consensus integrity under attack

Examples:
  $0                      # Test with 1 Byzantine node using delay faults
  $0 2 partition 600      # Test with 2 nodes partitioned for 10 minutes  
  $0 1 crash 300          # Test node crash and recovery
  $0 2 intermittent 900   # Test intermittent faults for 15 minutes

Results:
  All results saved in ../results/byzantine_test_* directories
  Manual log analysis required to verify Byzantine fault tolerance

EOF
    exit 0
fi

# Run main function
main "$@"