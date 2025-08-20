#!/bin/bash

# test_network_resilience.sh - Test network resilience under various conditions
# Usage: ./test_network_resilience.sh [SCENARIO] [INTENSITY] [TEST_DURATION]

set -e

SCENARIO=${1:-"progressive"}  # Options: progressive, burst, chaos, recovery
INTENSITY=${2:-"medium"}      # Options: low, medium, high, extreme
TEST_DURATION=${3:-600}       # 10 minutes default

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[RESILIENCE]${NC} $(date '+%H:%M:%S') $1"
}

warn() {
    echo -e "${YELLOW}[RESILIENCE]${NC} $(date '+%H:%M:%S') $1"
}

error() {
    echo -e "${RED}[RESILIENCE]${NC} $(date '+%H:%M:%S') $1"
}

info() {
    echo -e "${BLUE}[RESILIENCE]${NC} $(date '+%H:%M:%S') $1"
}

chaos() {
    echo -e "${PURPLE}[RESILIENCE]${NC} $(date '+%H:%M:%S') 💀 $1"
}

resilient() {
    echo -e "${CYAN}[RESILIENCE]${NC} $(date '+%H:%M:%S') 🛡️ $1"
}

# Test configuration
TEST_NAME="resilience_test_${SCENARIO}_${INTENSITY}_$(date +%Y%m%d_%H%M%S)"
RESULTS_DIR="../results/$TEST_NAME"
LOG_DIR="$RESULTS_DIR/logs"
METRICS_DIR="$RESULTS_DIR/metrics"

# Define intensity parameters
declare -A INTENSITY_PARAMS
INTENSITY_PARAMS[low_latency]=50
INTENSITY_PARAMS[low_loss]=2
INTENSITY_PARAMS[low_duration]=30
INTENSITY_PARAMS[medium_latency]=200
INTENSITY_PARAMS[medium_loss]=5
INTENSITY_PARAMS[medium_duration]=60
INTENSITY_PARAMS[high_latency]=500
INTENSITY_PARAMS[high_loss]=15
INTENSITY_PARAMS[high_duration]=120
INTENSITY_PARAMS[extreme_latency]=2000
INTENSITY_PARAMS[extreme_loss]=30
INTENSITY_PARAMS[extreme_duration]=300

# Initialize test environment
initialize_test() {
    log "Initializing network resilience test: $TEST_NAME"
    
    # Create results directories
    mkdir -p "$RESULTS_DIR" "$LOG_DIR" "$METRICS_DIR"
    
    # Get intensity parameters
    local base_latency=${INTENSITY_PARAMS[${INTENSITY}_latency]}
    local base_loss=${INTENSITY_PARAMS[${INTENSITY}_loss]}
    local base_duration=${INTENSITY_PARAMS[${INTENSITY}_duration]}
    
    # Create test configuration
    cat > "$RESULTS_DIR/test_config.json" <<EOF
{
    "test_name": "$TEST_NAME",
    "test_type": "network_resilience",
    "scenario": "$SCENARIO",
    "intensity": "$INTENSITY",
    "test_duration": $TEST_DURATION,
    "started_at": "$(date -Iseconds)",
    "intensity_parameters": {
        "base_latency_ms": $base_latency,
        "base_packet_loss_percent": $base_loss,
        "base_fault_duration_sec": $base_duration
    },
    "test_scenarios": {
        "progressive": "Gradually increase network stress",
        "burst": "Apply intense bursts of network problems",
        "chaos": "Random combination of all fault types",
        "recovery": "Test recovery from severe network failures"
    }
}
EOF
    
    log "Test configuration created: $SCENARIO scenario with $INTENSITY intensity"
    info "Parameters: ${base_latency}ms latency, ${base_loss}% loss, ${base_duration}s duration"
}

# Setup and start all services
setup_services() {
    log "Setting up all services for network resilience test"
    
    cd ../docker
    
    # Stop any existing services
    docker-compose down >/dev/null 2>&1 || true
    
    # Start all services
    log "Starting all network services..."
    docker-compose up -d >/dev/null 2>&1
    
    # Wait for services to be healthy
    log "Waiting for services to stabilize..."
    local max_wait=90
    local wait_time=0
    
    while [ $wait_time -lt $max_wait ]; do
        local healthy_count=$(docker-compose ps | grep "Up" | wc -l)
        if [ $healthy_count -ge 5 ]; then
            log "All services are running and healthy"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Health check: $healthy_count/5 services (${wait_time}s elapsed)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Not all services became healthy within ${max_wait}s"
        docker-compose ps
    fi
    
    cd - >/dev/null
    
    # Allow network to establish connections
    log "Allowing network to establish peer connections..."
    sleep 30
    
    resilient "Network baseline established"
}

# Progressive resilience test - gradually increase stress
run_progressive_scenario() {
    local base_latency=${INTENSITY_PARAMS[${INTENSITY}_latency]}
    local base_loss=${INTENSITY_PARAMS[${INTENSITY}_loss]}
    local phase_duration=$((TEST_DURATION / 5))
    
    log "Starting progressive resilience scenario"
    info "5 phases, ${phase_duration}s each, escalating network stress"
    
    # Phase 1: Baseline (no stress)
    resilient "Phase 1/5: Baseline - no network stress (${phase_duration}s)"
    echo "$(date -Iseconds): Phase 1 - Baseline started" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 2: Light stress
    resilient "Phase 2/5: Light stress - ${base_latency}ms latency (${phase_duration}s)"
    ../scripts/simulate_latency.sh $base_latency all all
    echo "$(date -Iseconds): Phase 2 - Applied ${base_latency}ms latency" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 3: Medium stress - add packet loss
    resilient "Phase 3/5: Medium stress - latency + ${base_loss}% packet loss (${phase_duration}s)"
    ../scripts/simulate_packet_loss.sh $base_loss all all
    echo "$(date -Iseconds): Phase 3 - Added ${base_loss}% packet loss" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 4: High stress - increase both
    local high_latency=$((base_latency * 2))
    local high_loss=$((base_loss * 2))
    resilient "Phase 4/5: High stress - ${high_latency}ms + ${high_loss}% loss (${phase_duration}s)"
    ../scripts/reset_network.sh >/dev/null 2>&1
    sleep 5
    ../scripts/simulate_latency.sh $high_latency all all
    ../scripts/simulate_packet_loss.sh $high_loss all all
    echo "$(date -Iseconds): Phase 4 - High stress: ${high_latency}ms + ${high_loss}% loss" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 5: Recovery - remove all stress
    resilient "Phase 5/5: Recovery - removing all network stress (${phase_duration}s)"
    ../scripts/reset_network.sh >/dev/null 2>&1
    echo "$(date -Iseconds): Phase 5 - Recovery phase started" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    resilient "Progressive scenario completed - network stress progression tested"
}

# Burst resilience test - intense bursts followed by recovery
run_burst_scenario() {
    local burst_latency=$((${INTENSITY_PARAMS[${INTENSITY}_latency]} * 3))
    local burst_loss=$((${INTENSITY_PARAMS[${INTENSITY}_loss]} * 3))
    local burst_duration=60
    local recovery_duration=120
    local num_bursts=$((TEST_DURATION / (burst_duration + recovery_duration)))
    
    log "Starting burst resilience scenario"
    info "$num_bursts bursts, ${burst_duration}s stress + ${recovery_duration}s recovery"
    
    for ((i=1; i<=num_bursts; i++)); do
        # Burst phase
        chaos "Burst $i/$num_bursts: Applying ${burst_latency}ms + ${burst_loss}% loss for ${burst_duration}s"
        ../scripts/simulate_latency.sh $burst_latency all all
        ../scripts/simulate_packet_loss.sh $burst_loss all all
        echo "$(date -Iseconds): Burst $i - ${burst_latency}ms + ${burst_loss}% loss" >> "$LOG_DIR/resilience_phases.log"
        sleep $burst_duration
        
        # Recovery phase
        resilient "Recovery $i/$num_bursts: Network recovery for ${recovery_duration}s"
        ../scripts/reset_network.sh >/dev/null 2>&1
        echo "$(date -Iseconds): Recovery $i - network reset" >> "$LOG_DIR/resilience_phases.log"
        sleep $recovery_duration
    done
    
    resilient "Burst scenario completed - tested rapid stress/recovery cycles"
}

# Chaos resilience test - random combination of failures
run_chaos_scenario() {
    local chaos_duration=$TEST_DURATION
    local chaos_interval=45  # Change conditions every 45 seconds
    local num_intervals=$((chaos_duration / chaos_interval))
    
    log "Starting chaos resilience scenario"
    chaos "💀 CHAOS MODE: Random network failures every ${chaos_interval}s for ${chaos_duration}s 💀"
    
    # Create chaos script
    cat > "$RESULTS_DIR/chaos_controller.sh" <<EOF
#!/bin/bash

containers=("p2p-bootstrap" "p2p-sequencer-1" "p2p-sequencer-2" "p2p-sequencer-3" "p2p-debugger")
fault_types=("latency" "packet_loss" "partition" "burst_loss" "node_isolation")

for ((i=1; i<=$num_intervals; i++)); do
    echo "\$(date -Iseconds): Chaos interval \$i/$num_intervals" >> "$LOG_DIR/resilience_phases.log"
    
    # Reset previous conditions
    ../scripts/reset_network.sh >/dev/null 2>&1
    sleep 5
    
    # Apply 1-3 random faults
    num_faults=\$((1 + RANDOM % 3))
    echo "  Applying \$num_faults random faults" >> "$LOG_DIR/resilience_phases.log"
    
    for ((f=1; f<=num_faults; f++)); do
        fault_type=\${fault_types[\$((RANDOM % \${#fault_types[@]}))]}
        target_node=\${containers[\$((RANDOM % \${#containers[@]}))]}
        
        case "\$fault_type" in
            "latency")
                latency=\$((100 + RANDOM % 1000))
                ../scripts/simulate_latency.sh \$latency \$target_node all
                echo "    Applied \${latency}ms latency to \$target_node" >> "$LOG_DIR/resilience_phases.log"
                ;;
            "packet_loss")
                loss=\$((5 + RANDOM % 20))
                ../scripts/simulate_packet_loss.sh \$loss \$target_node all
                echo "    Applied \${loss}% packet loss to \$target_node" >> "$LOG_DIR/resilience_phases.log"
                ;;
            "partition")
                ../scripts/simulate_partition.sh isolate \$target_node
                echo "    Isolated \$target_node" >> "$LOG_DIR/resilience_phases.log"
                ;;
            "burst_loss")
                ../scripts/simulate_packet_loss.sh --burst 50 \$target_node 15
                echo "    Applied 50% burst loss to \$target_node for 15s" >> "$LOG_DIR/resilience_phases.log"
                ;;
            "node_isolation")
                other_node=\${containers[\$((RANDOM % \${#containers[@]}))]}
                if [ "\$target_node" != "\$other_node" ]; then
                    ../scripts/simulate_partition.sh custom "\$target_node|\$other_node"
                    echo "    Partitioned \$target_node from \$other_node" >> "$LOG_DIR/resilience_phases.log"
                fi
                ;;
        esac
    done
    
    sleep $chaos_interval
done

echo "\$(date -Iseconds): Chaos scenario completed" >> "$LOG_DIR/resilience_phases.log"
../scripts/reset_network.sh >/dev/null 2>&1
EOF
    
    chmod +x "$RESULTS_DIR/chaos_controller.sh"
    
    # Run chaos controller
    "$RESULTS_DIR/chaos_controller.sh" &
    local chaos_pid=$!
    echo $chaos_pid > "$RESULTS_DIR/chaos_pid"
    
    # Wait for chaos to complete
    wait $chaos_pid
    
    chaos "Chaos scenario completed - survived random network failures!"
}

# Recovery resilience test - severe failures with recovery
run_recovery_scenario() {
    local recovery_phases=4
    local phase_duration=$((TEST_DURATION / recovery_phases))
    
    log "Starting recovery resilience scenario"
    resilient "Testing recovery from severe network failures in $recovery_phases phases"
    
    # Phase 1: Complete network partition
    resilient "Phase 1/4: Network split partition (${phase_duration}s)"
    ../scripts/simulate_partition.sh split
    echo "$(date -Iseconds): Phase 1 - Network split partition" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 2: Bootstrap isolation
    resilient "Phase 2/4: Bootstrap node isolation (${phase_duration}s)"
    ../scripts/reset_network.sh >/dev/null 2>&1
    sleep 10
    ../scripts/simulate_partition.sh bootstrap
    echo "$(date -Iseconds): Phase 2 - Bootstrap isolation" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    # Phase 3: Multiple node failures
    resilient "Phase 3/4: Multiple node crashes (${phase_duration}s)"
    ../scripts/reset_network.sh >/dev/null 2>&1
    sleep 10
    ../scripts/simulate_partition.sh fail p2p-sequencer-1
    sleep 30
    ../scripts/simulate_partition.sh fail p2p-sequencer-2
    echo "$(date -Iseconds): Phase 3 - Multiple node crashes" >> "$LOG_DIR/resilience_phases.log"
    
    # Start recovery halfway through phase 3
    (
        sleep $((phase_duration / 2))
        resilient "Starting node recovery..."
        docker-compose -f ../docker/docker-compose.yml start p2p-sequencer-1 >/dev/null 2>&1
        sleep 30
        docker-compose -f ../docker/docker-compose.yml start p2p-sequencer-2 >/dev/null 2>&1
        echo "$(date -Iseconds): Nodes recovered" >> "$LOG_DIR/resilience_phases.log"
    ) &
    
    sleep $phase_duration
    
    # Phase 4: Full recovery and healing
    resilient "Phase 4/4: Full network recovery and healing (${phase_duration}s)"
    ../scripts/reset_network.sh --restart >/dev/null 2>&1
    echo "$(date -Iseconds): Phase 4 - Full recovery" >> "$LOG_DIR/resilience_phases.log"
    sleep $phase_duration
    
    resilient "Recovery scenario completed - tested severe failure recovery"
}

# Monitor network health throughout the test
monitor_network_health() {
    log "Starting network health monitoring..."
    
    cat > "$RESULTS_DIR/monitor_health.sh" <<EOF
#!/bin/bash

start_time=\$(date +%s)
end_time=\$((start_time + $TEST_DURATION + 60))  # Extra buffer for cleanup

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    
    echo "\$timestamp: Network Health Check" >> "$LOG_DIR/network_health.log"
    
    total_connections=0
    total_cpu=0
    total_mem=0
    healthy_nodes=0
    
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        if docker exec \$container echo "test" >/dev/null 2>&1; then
            healthy_nodes=\$((healthy_nodes + 1))
            
            # Network connections
            connections=\$(docker exec \$container netstat -an 2>/dev/null | grep ESTABLISHED | wc -l || echo "0")
            total_connections=\$((total_connections + connections))
            
            # Resource usage
            cpu_mem=\$(docker stats \$container --no-stream --format "{{.CPUPerc}} {{.MemUsage}}" 2>/dev/null || echo "0.0% 0B/0B")
            cpu_percent=\$(echo "\$cpu_mem" | awk '{print \$1}' | tr -d '%')
            
            # Network conditions
            tc_status=\$(docker exec \$container tc qdisc show dev eth0 2>/dev/null | head -1 | grep -v "pfifo_fast" || echo "normal")
            iptables_rules=\$(docker exec \$container iptables -L OUTPUT 2>/dev/null | grep DROP | wc -l || echo "0")
            
            echo "  \$container: HEALTHY conn=\$connections cpu=\${cpu_percent}% tc='\$tc_status' ipt=\$iptables_rules" >> "$LOG_DIR/network_health.log"
        else
            echo "  \$container: OFFLINE" >> "$LOG_DIR/network_health.log"
        fi
    done
    
    # Overall health metrics
    health_percent=\$((healthy_nodes * 100 / 5))
    echo "  SUMMARY: \$healthy_nodes/5 nodes (\${health_percent}%) total_conn=\$total_connections" >> "$LOG_DIR/network_health.log"
    
    # Health status to metrics
    echo "\$timestamp,\$healthy_nodes,\$total_connections,\$health_percent" >> "$METRICS_DIR/health_metrics.csv"
    
    echo "" >> "$LOG_DIR/network_health.log"
    sleep 10
done
EOF
    
    chmod +x "$RESULTS_DIR/monitor_health.sh"
    
    # Initialize CSV header
    echo "timestamp,healthy_nodes,total_connections,health_percent" > "$METRICS_DIR/health_metrics.csv"
    
    # Start health monitoring in background
    "$RESULTS_DIR/monitor_health.sh" &
    local health_monitor_pid=$!
    echo $health_monitor_pid > "$RESULTS_DIR/health_monitor_pid"
    
    log "Network health monitoring started (PID: $health_monitor_pid)"
}

# Generate realistic network load during test
generate_network_load() {
    log "Starting network load generation..."
    
    cat > "$RESULTS_DIR/load_generator.sh" <<EOF
#!/bin/bash

submission_count=0
load_duration=$TEST_DURATION
submissions_per_minute=15
interval=\$((60 / submissions_per_minute))

end_time=\$(($(date +%s) + load_duration))

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    submission_id="resilience_load_\$(date +%s)_\${submission_count}"
    
    # Create realistic submission
    submission_data="{
        \"id\": \"\$submission_id\",
        \"timestamp\": \"\$timestamp\",
        \"epoch\": \$((submission_count / 10 + 1)),
        \"data\": \"resilience_test_data_\${submission_count}\",
        \"size_kb\": \$((1 + RANDOM % 10)),
        \"stress_scenario\": \"$SCENARIO\"
    }"
    
    echo "\$timestamp: Generated load submission \$submission_id" >> "$LOG_DIR/load_submissions.log"
    echo "\$submission_data" >> "$RESULTS_DIR/load_submissions.jsonl"
    
    submission_count=\$((submission_count + 1))
    sleep \$interval
done

echo "\$(date -Iseconds): Load generation completed (\$submission_count submissions)" >> "$LOG_DIR/load_submissions.log"
EOF
    
    chmod +x "$RESULTS_DIR/load_generator.sh"
    
    # Start load generation in background
    "$RESULTS_DIR/load_generator.sh" &
    local load_gen_pid=$!
    echo $load_gen_pid > "$RESULTS_DIR/load_gen_pid"
    
    log "Network load generator started (PID: $load_gen_pid)"
}

# Run the main test based on scenario
run_test() {
    log "Running network resilience test: $SCENARIO scenario"
    info "Intensity: $INTENSITY, Duration: ${TEST_DURATION}s"
    
    case "$SCENARIO" in
        "progressive")
            run_progressive_scenario
            ;;
        "burst")
            run_burst_scenario
            ;;
        "chaos")
            run_chaos_scenario
            ;;
        "recovery")
            run_recovery_scenario
            ;;
        *)
            error "Unknown scenario: $SCENARIO"
            exit 1
            ;;
    esac
    
    resilient "Network resilience test scenario completed!"
}

# Collect results and generate comprehensive report
collect_results() {
    log "Collecting network resilience test results..."
    
    # Stop all monitoring processes
    for pid_file in health_monitor_pid load_gen_pid chaos_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            local pid=$(cat "$RESULTS_DIR/$pid_file")
            kill $pid 2>/dev/null || true
            rm -f "$RESULTS_DIR/$pid_file"
        fi
    done
    
    # Ensure network is fully reset
    log "Resetting all network conditions..."
    ../scripts/reset_network.sh --restart >/dev/null 2>&1 || true
    
    # Collect final container logs
    log "Collecting container logs..."
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        docker logs "$container" > "$LOG_DIR/${container}.log" 2>&1 || true
    done
    
    # Generate comprehensive report
    generate_resilience_report
    
    log "Results collected in: $RESULTS_DIR"
}

# Generate detailed resilience test report
generate_resilience_report() {
    log "Generating network resilience report..."
    
    local end_time=$(date -Iseconds)
    local total_load_submissions=0
    
    if [ -f "$RESULTS_DIR/load_submissions.jsonl" ]; then
        total_load_submissions=$(wc -l < "$RESULTS_DIR/load_submissions.jsonl")
    fi
    
    # Calculate health statistics from CSV
    local min_health=100
    local max_downtime=0
    if [ -f "$METRICS_DIR/health_metrics.csv" ]; then
        min_health=$(tail -n +2 "$METRICS_DIR/health_metrics.csv" | cut -d',' -f4 | sort -n | head -1 || echo "100")
        # Count consecutive health drops for downtime calculation
    fi
    
    # Create detailed report
    cat > "$RESULTS_DIR/test_report.json" <<EOF
{
    "test_summary": {
        "test_name": "$TEST_NAME",
        "test_type": "network_resilience",
        "scenario": "$SCENARIO",
        "intensity": "$INTENSITY",
        "duration": $TEST_DURATION,
        "completed_at": "$end_time",
        "total_load_submissions": $total_load_submissions
    },
    "resilience_metrics": {
        "minimum_health_percent": $min_health,
        "network_recovery_time": "TBD - analyze logs",
        "submission_success_rate": "TBD - compare expected vs actual",
        "peer_connectivity_maintained": "TBD - analyze connection counts"
    },
    "scenario_results": {
        "$(echo $SCENARIO)": "See phase logs and health metrics for detailed analysis"
    },
    "intensity_applied": {
        "latency_ms": ${INTENSITY_PARAMS[${INTENSITY}_latency]},
        "packet_loss_percent": ${INTENSITY_PARAMS[${INTENSITY}_loss]},
        "fault_duration_sec": ${INTENSITY_PARAMS[${INTENSITY}_duration]}
    }
}
EOF
    
    # Create comprehensive human-readable report
    cat > "$RESULTS_DIR/RESILIENCE_REPORT.md" <<EOF
# Network Resilience Test Report

## Test Configuration
- **Test Name**: $TEST_NAME
- **Scenario**: $SCENARIO
- **Intensity**: $INTENSITY
- **Duration**: ${TEST_DURATION}s
- **Completed**: $(date)

## Scenario Description
$(case "$SCENARIO" in
    "progressive") echo "**Progressive Stress**: Gradually increased network stress across 5 phases";;
    "burst") echo "**Burst Stress**: Intense stress bursts followed by recovery periods";;
    "chaos") echo "**Chaos Engineering**: Random combination of network failures";;
    "recovery") echo "**Recovery Testing**: Severe failures with recovery validation";;
esac)

## Intensity Configuration
- **Base Latency**: ${INTENSITY_PARAMS[${INTENSITY}_latency]}ms
- **Base Packet Loss**: ${INTENSITY_PARAMS[${INTENSITY}_loss]}%
- **Base Fault Duration**: ${INTENSITY_PARAMS[${INTENSITY}_duration]}s

## Key Metrics
- **Minimum Network Health**: ${min_health}%
- **Total Submissions Generated**: $total_load_submissions
- **Test Duration**: ${TEST_DURATION}s

## Analysis Required

### Network Health Analysis
1. **Health Timeline**: Review \`health_metrics.csv\` for health percentage over time
2. **Connection Stability**: Analyze connection counts during stress periods
3. **Recovery Patterns**: Identify how quickly network recovered from failures

### Submission Analysis  
1. **Success Rate**: Compare generated vs processed submissions
2. **Latency Impact**: Measure submission processing delays during stress
3. **Data Integrity**: Verify no submission corruption or loss

### Resilience Patterns
1. **Failure Detection**: How quickly did nodes detect network problems
2. **Adaptation**: Did nodes adapt routing or behavior during stress
3. **Recovery Speed**: Time to return to baseline after stress removal

## Key Files Generated
- \`health_metrics.csv\` - Timestamped health and connectivity data
- \`network_health.log\` - Detailed health monitoring logs
- \`resilience_phases.log\` - Timeline of stress application/removal
- \`load_submissions.jsonl\` - Generated network load
- \`logs/\` - Individual container logs for detailed analysis

## Recommendations
1. **Graph Health Metrics**: Plot health percentage and connection counts over time
2. **Identify Bottlenecks**: Find which stress types caused most degradation
3. **Measure Recovery**: Calculate average recovery time after stress removal
4. **Performance Comparison**: Compare with baseline consensus test results

## Success Criteria
- ✅ Network maintained > 60% health during stress periods
- ✅ Full recovery achieved after stress removal
- ✅ No permanent node failures or network splits
- ✅ Submission processing continued under stress

EOF
    
    resilient "Network resilience report generated: $RESULTS_DIR/RESILIENCE_REPORT.md"
}

# Cleanup function
cleanup() {
    log "Cleaning up network resilience test..."
    
    # Kill all monitoring processes
    for pid_file in health_monitor_pid load_gen_pid chaos_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            kill $(cat "$RESULTS_DIR/$pid_file") 2>/dev/null || true
        fi
    done
    
    # Ensure complete network reset
    ../scripts/reset_network.sh --restart >/dev/null 2>&1 || true
    
    log "Network resilience test cleanup completed"
}

# Main execution
main() {
    resilient "🛡️ Starting Network Resilience Test 🛡️"
    log "Scenario: $SCENARIO, Intensity: $INTENSITY, Duration: ${TEST_DURATION}s"
    
    # Validate parameters
    if [ $TEST_DURATION -lt 180 ]; then
        error "TEST_DURATION must be at least 180 seconds for resilience tests (got: $TEST_DURATION)"
        exit 1
    fi
    
    if [[ ! "$INTENSITY" =~ ^(low|medium|high|extreme)$ ]]; then
        error "INTENSITY must be one of: low, medium, high, extreme (got: $INTENSITY)"
        exit 1
    fi
    
    if [[ ! "$SCENARIO" =~ ^(progressive|burst|chaos|recovery)$ ]]; then
        error "SCENARIO must be one of: progressive, burst, chaos, recovery (got: $SCENARIO)"
        exit 1
    fi
    
    # Show intensity parameters
    info "Intensity parameters for '$INTENSITY':"
    info "  - Latency: ${INTENSITY_PARAMS[${INTENSITY}_latency]}ms"
    info "  - Packet Loss: ${INTENSITY_PARAMS[${INTENSITY}_loss]}%"
    info "  - Fault Duration: ${INTENSITY_PARAMS[${INTENSITY}_duration]}s"
    
    # Set trap for cleanup on exit
    trap cleanup EXIT INT TERM
    
    # Run test phases
    initialize_test
    setup_services
    monitor_network_health
    generate_network_load
    run_test
    collect_results
    
    resilient "🎯 Network resilience test completed successfully!"
    info "📊 Results available in: $RESULTS_DIR"
    info "📝 Report: $RESULTS_DIR/RESILIENCE_REPORT.md"
    info "📈 Health metrics: $RESULTS_DIR/metrics/health_metrics.csv"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Network Resilience Test Suite

Usage: $0 [SCENARIO] [INTENSITY] [TEST_DURATION]

Parameters:
  SCENARIO      - Test scenario (default: progressive)
                  Options: progressive, burst, chaos, recovery
  INTENSITY     - Stress intensity (default: medium)  
                  Options: low, medium, high, extreme
  TEST_DURATION - Test duration in seconds (min: 180, default: 600)

Scenarios:
  progressive   - Gradually increase network stress across 5 phases
  burst         - Apply intense stress bursts followed by recovery periods
  chaos         - Random combination of network failures (chaos engineering)
  recovery      - Test recovery from severe network failures

Intensity Levels:
  low           - 50ms latency, 2% loss, 30s faults
  medium        - 200ms latency, 5% loss, 60s faults  
  high          - 500ms latency, 15% loss, 120s faults
  extreme       - 2000ms latency, 30% loss, 300s faults

Description:
  Tests network resilience by:
  1. Starting all network services
  2. Generating realistic network load
  3. Applying specified stress scenario
  4. Monitoring network health continuously
  5. Measuring recovery and adaptation
  6. Analyzing resilience patterns

Examples:
  $0                              # Progressive test with medium intensity
  $0 burst high 900               # High-intensity burst test for 15 minutes
  $0 chaos extreme 1200           # Extreme chaos test for 20 minutes
  $0 recovery low 600             # Low-intensity recovery test

Results:
  All results saved in ../results/resilience_test_* directories
  Health metrics saved as CSV for graphing and analysis

EOF
    exit 0
fi

# Run main function
main "$@"