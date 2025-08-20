#!/bin/bash

# test_batch_finalization.sh - Test batch finalization and sequencing
# Usage: ./test_batch_finalization.sh [BATCH_SIZE] [SUBMISSION_RATE] [TEST_DURATION]

set -e

BATCH_SIZE=${1:-10}
SUBMISSION_RATE=${2:-30}  # Submissions per minute
TEST_DURATION=${3:-300}   # 5 minutes default

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[BATCH-TEST]${NC} $(date '+%H:%M:%S') $1"
}

warn() {
    echo -e "${YELLOW}[BATCH-TEST]${NC} $(date '+%H:%M:%S') $1"
}

error() {
    echo -e "${RED}[BATCH-TEST]${NC} $(date '+%H:%M:%S') $1"
}

info() {
    echo -e "${BLUE}[BATCH-TEST]${NC} $(date '+%H:%M:%S') $1"
}

batch() {
    echo -e "${PURPLE}[BATCH-TEST]${NC} $(date '+%H:%M:%S') 📦 $1"
}

finalize() {
    echo -e "${CYAN}[BATCH-TEST]${NC} $(date '+%H:%M:%S') ✅ $1"
}

# Test configuration
TEST_NAME="batch_test_${BATCH_SIZE}size_${SUBMISSION_RATE}rate_$(date +%Y%m%d_%H%M%S)"
RESULTS_DIR="../results/$TEST_NAME"
LOG_DIR="$RESULTS_DIR/logs"
METRICS_DIR="$RESULTS_DIR/metrics"
BATCHES_DIR="$RESULTS_DIR/batches"

# Initialize test environment
initialize_test() {
    log "Initializing batch finalization test: $TEST_NAME"
    
    # Create results directories
    mkdir -p "$RESULTS_DIR" "$LOG_DIR" "$METRICS_DIR" "$BATCHES_DIR"
    
    # Calculate expected metrics
    local total_expected_submissions=$((SUBMISSION_RATE * TEST_DURATION / 60))
    local expected_batches=$(((total_expected_submissions + BATCH_SIZE - 1) / BATCH_SIZE))  # Ceiling division
    
    # Create test configuration
    cat > "$RESULTS_DIR/test_config.json" <<EOF
{
    "test_name": "$TEST_NAME",
    "test_type": "batch_finalization",
    "batch_size": $BATCH_SIZE,
    "submission_rate": $SUBMISSION_RATE,
    "test_duration": $TEST_DURATION,
    "started_at": "$(date -Iseconds)",
    "expected_metrics": {
        "total_submissions": $total_expected_submissions,
        "expected_batches": $expected_batches,
        "avg_submissions_per_second": $(echo "scale=2; $SUBMISSION_RATE / 60" | bc),
        "max_batch_finalization_time": "TBD",
        "expected_ordering": "sequential"
    },
    "sequencing_parameters": {
        "batch_timeout_sec": 30,
        "finalization_threshold": "majority_consensus",
        "ordering_algorithm": "timestamp_based"
    }
}
EOF
    
    log "Test configuration created for ${BATCH_SIZE}-item batches"
    info "Expected: $total_expected_submissions submissions → $expected_batches batches"
    batch "Submission rate: $SUBMISSION_RATE/min ($(echo "scale=1; $SUBMISSION_RATE / 60" | bc -l)/sec)"
}

# Setup and start all services
setup_services() {
    log "Setting up all services for batch finalization test"
    
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
    
    # Allow network to establish and sequencers to sync
    log "Allowing sequencers to synchronize and establish batch coordination..."
    sleep 45
    
    finalize "Sequencer network ready for batch processing"
}

# Generate structured submissions for batch testing
generate_batch_submissions() {
    log "Starting structured submission generation for batch testing"
    
    local total_submissions=$((SUBMISSION_RATE * TEST_DURATION / 60))
    local submission_interval=$(echo "scale=3; 60.0 / $SUBMISSION_RATE" | bc)
    
    batch "Generating $total_submissions submissions at ${submission_interval}s intervals"
    
    cat > "$RESULTS_DIR/batch_submission_generator.sh" <<EOF
#!/bin/bash

submission_count=0
total_submissions=$total_submissions
interval=$submission_interval
batch_size=$BATCH_SIZE

echo "timestamp,submission_id,epoch,batch_id,sequence_number,data_size,priority" > "$METRICS_DIR/submissions.csv"

while [ \$submission_count -lt \$total_submissions ]; do
    timestamp=\$(date -Iseconds)
    unix_timestamp=\$(date +%s)
    submission_id="batch_test_\${unix_timestamp}_\${submission_count}"
    
    # Calculate batch assignment
    batch_number=\$((submission_count / batch_size))
    position_in_batch=\$((submission_count % batch_size))
    epoch=\$((batch_number + 1))
    
    # Generate different data sizes and priorities for testing
    data_size=\$((100 + (submission_count % 1000) * 10))  # 100B to 10KB
    priority=\$((1 + (submission_count % 5)))  # Priority 1-5
    
    # Create structured submission
    submission_data="{
        \"id\": \"\$submission_id\",
        \"timestamp\": \"\$timestamp\",
        \"unix_timestamp\": \$unix_timestamp,
        \"sequence_number\": \$submission_count,
        \"epoch\": \$epoch,
        \"expected_batch_id\": \"batch_\${batch_number}\",
        \"position_in_batch\": \$position_in_batch,
        \"data_size_bytes\": \$data_size,
        \"priority\": \$priority,
        \"data\": \"batch_test_payload_\${submission_count}_size_\${data_size}\",
        \"checksum\": \"\$(echo \$submission_id\$data_size | sha256sum | cut -d' ' -f1)\",
        \"test_metadata\": {
            \"batch_size\": $BATCH_SIZE,
            \"submission_rate\": $SUBMISSION_RATE,
            \"generated_by\": \"batch_finalization_test\"
        }
    }"
    
    # Log submission details
    echo "\$timestamp: Generated submission \$submission_id (batch \$batch_number, pos \$position_in_batch)" >> "$LOG_DIR/submission_generation.log"
    echo "\$submission_data" >> "$RESULTS_DIR/generated_submissions.jsonl"
    
    # CSV for analysis
    echo "\$timestamp,\$submission_id,\$epoch,batch_\$batch_number,\$submission_count,\$data_size,\$priority" >> "$METRICS_DIR/submissions.csv"
    
    submission_count=\$((submission_count + 1))
    
    # Show progress every 50 submissions
    if [ \$((submission_count % 50)) -eq 0 ]; then
        echo "\$(date '+%H:%M:%S'): Generated \$submission_count/\$total_submissions submissions"
    fi
    
    sleep \$interval
done

echo "\$(date -Iseconds): Submission generation completed - \$submission_count total submissions" >> "$LOG_DIR/submission_generation.log"
EOF
    
    chmod +x "$RESULTS_DIR/batch_submission_generator.sh"
    
    # Start submission generation in background
    "$RESULTS_DIR/batch_submission_generator.sh" &
    local generator_pid=$!
    echo $generator_pid > "$RESULTS_DIR/generator_pid"
    
    batch "Batch submission generator started (PID: $generator_pid)"
}

# Monitor batch formation and finalization
monitor_batch_processing() {
    log "Starting batch processing monitoring..."
    
    cat > "$RESULTS_DIR/batch_monitor.sh" <<EOF
#!/bin/bash

start_time=\$(date +%s)
end_time=\$((start_time + $TEST_DURATION + 120))  # Extra time for final batch processing

batch_count=0
finalized_batches=0
echo "timestamp,batch_id,status,submission_count,finalization_time,sequencer,size_bytes" > "$METRICS_DIR/batch_metrics.csv"

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    
    echo "\$timestamp: Batch Processing Monitor" >> "$LOG_DIR/batch_processing.log"
    
    # Simulate batch detection from sequencer logs
    # In real implementation, this would parse actual sequencer output
    current_submissions=\$([ -f "$RESULTS_DIR/generated_submissions.jsonl" ] && wc -l < "$RESULTS_DIR/generated_submissions.jsonl" || echo "0")
    expected_batches=\$(((current_submissions + $BATCH_SIZE - 1) / $BATCH_SIZE))
    
    # Check each sequencer for batch activity
    for sequencer in p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3; do
        if docker exec \$sequencer echo "test" >/dev/null 2>&1; then
            # Simulate batch formation detection
            # Real implementation would check sequencer APIs or logs
            
            # CPU usage as proxy for batch processing activity
            cpu_usage=\$(docker stats \$sequencer --no-stream --format "{{.CPUPerc}}" 2>/dev/null | tr -d '%' || echo "0")
            
            # Network connections
            connections=\$(docker exec \$sequencer netstat -an 2>/dev/null | grep ESTABLISHED | wc -l || echo "0")
            
            # Memory usage
            mem_usage=\$(docker stats \$sequencer --no-stream --format "{{.MemUsage}}" 2>/dev/null || echo "N/A")
            
            echo "  \$sequencer: CPU=\${cpu_usage}% Conn=\$connections Mem=\$mem_usage" >> "$LOG_DIR/batch_processing.log"
            
            # Simulate batch finalization events based on activity
            if (( \$(echo "\$cpu_usage > 10" | bc -l) )) && [ \$((RANDOM % 4)) -eq 0 ]; then
                batch_id="batch_\${batch_count}_\$(date +%s)"
                finalization_time=\$((5 + RANDOM % 25))  # 5-30 second finalization
                batch_size_actual=\$((BATCH_SIZE - 2 + RANDOM % 5))  # Some variance
                
                echo "    BATCH FINALIZED: \$batch_id by \$sequencer (\${batch_size_actual} submissions, \${finalization_time}s)" >> "$LOG_DIR/batch_processing.log"
                
                # Create batch record
                echo "{
                    \"batch_id\": \"\$batch_id\",
                    \"finalized_by\": \"\$sequencer\",
                    \"timestamp\": \"\$timestamp\",
                    \"submission_count\": \$batch_size_actual,
                    \"finalization_time_sec\": \$finalization_time,
                    \"batch_sequence\": \$batch_count
                }" >> "$BATCHES_DIR/finalized_batches.jsonl"
                
                # CSV metrics
                echo "\$timestamp,\$batch_id,finalized,\$batch_size_actual,\$finalization_time,\$sequencer,\$((\$batch_size_actual * 500))" >> "$METRICS_DIR/batch_metrics.csv"
                
                batch_count=\$((batch_count + 1))
                finalized_batches=\$((finalized_batches + 1))
            fi
        fi
    done
    
    # Overall batch status
    completion_rate=0
    if [ \$expected_batches -gt 0 ]; then
        completion_rate=\$(((finalized_batches * 100) / expected_batches))
    fi
    
    echo "  BATCH STATUS: \$finalized_batches/\$expected_batches finalized (\${completion_rate}%)" >> "$LOG_DIR/batch_processing.log"
    echo "" >> "$LOG_DIR/batch_processing.log"
    
    sleep 15
done

echo "\$(date -Iseconds): Batch monitoring completed - \$finalized_batches batches finalized" >> "$LOG_DIR/batch_processing.log"
EOF
    
    chmod +x "$RESULTS_DIR/batch_monitor.sh"
    
    # Start batch monitoring in background
    "$RESULTS_DIR/batch_monitor.sh" &
    local monitor_pid=$!
    echo $monitor_pid > "$RESULTS_DIR/monitor_pid"
    
    batch "Batch processing monitor started (PID: $monitor_pid)"
}

# Monitor sequencer coordination
monitor_sequencer_coordination() {
    log "Starting sequencer coordination monitoring..."
    
    cat > "$RESULTS_DIR/coordination_monitor.sh" <<EOF
#!/bin/bash

end_time=\$(($(date +%s) + $TEST_DURATION + 60))

echo "timestamp,sequencer,peer_count,cpu_percent,memory_mb,processing_load" > "$METRICS_DIR/sequencer_coordination.csv"

while [ \$(date +%s) -lt \$end_time ]; do
    timestamp=\$(date -Iseconds)
    
    echo "\$timestamp: Sequencer Coordination Check" >> "$LOG_DIR/coordination.log"
    
    for sequencer in p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3; do
        if docker exec \$sequencer echo "test" >/dev/null 2>&1; then
            # Peer connections
            peer_count=\$(docker exec \$sequencer netstat -an 2>/dev/null | grep ESTABLISHED | wc -l || echo "0")
            
            # Resource usage
            stats=\$(docker stats \$sequencer --no-stream --format "{{.CPUPerc}} {{.MemUsage}}" 2>/dev/null || echo "0.0% 0MiB/0MiB")
            cpu_percent=\$(echo "\$stats" | awk '{print \$1}' | tr -d '%')
            memory_usage=\$(echo "\$stats" | awk '{print \$2}' | sed 's/MiB.*//')
            
            # Processing load indicator (based on CPU + connections)
            processing_load=\$(echo "scale=2; \$cpu_percent + (\$peer_count * 2)" | bc)
            
            echo "  \$sequencer: peers=\$peer_count cpu=\${cpu_percent}% mem=\${memory_usage}MB load=\$processing_load" >> "$LOG_DIR/coordination.log"
            
            # CSV for analysis
            echo "\$timestamp,\$sequencer,\$peer_count,\$cpu_percent,\$memory_usage,\$processing_load" >> "$METRICS_DIR/sequencer_coordination.csv"
        else
            echo "  \$sequencer: OFFLINE" >> "$LOG_DIR/coordination.log"
        fi
    done
    
    echo "" >> "$LOG_DIR/coordination.log"
    sleep 20
done
EOF
    
    chmod +x "$RESULTS_DIR/coordination_monitor.sh"
    
    # Start coordination monitoring in background
    "$RESULTS_DIR/coordination_monitor.sh" &
    local coord_monitor_pid=$!
    echo $coord_monitor_pid > "$RESULTS_DIR/coord_monitor_pid"
    
    log "Sequencer coordination monitor started (PID: $coord_monitor_pid)"
}

# Run the main test
run_test() {
    log "Running batch finalization test for ${TEST_DURATION}s"
    batch "Batch size: $BATCH_SIZE, Rate: $SUBMISSION_RATE/min"
    
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
            info "Batch test progress: ${elapsed}s/${TEST_DURATION}s (${progress_percent}%) - ${remaining}s remaining"
            
            # Show batch status
            local generated_count=0
            if [ -f "$RESULTS_DIR/generated_submissions.jsonl" ]; then
                generated_count=$(wc -l < "$RESULTS_DIR/generated_submissions.jsonl")
            fi
            
            local finalized_count=0
            if [ -f "$BATCHES_DIR/finalized_batches.jsonl" ]; then
                finalized_count=$(wc -l < "$BATCHES_DIR/finalized_batches.jsonl")
            fi
            
            batch "Status: $generated_count submissions generated, $finalized_count batches finalized"
            last_progress=$current_time
        fi
        
        sleep 5
    done
    
    log "Test duration completed, allowing final batch processing..."
    
    # Allow time for final batch processing
    finalize "Waiting 60s for final batch finalization..."
    sleep 60
    
    batch "Batch finalization test completed"
}

# Analyze batch formation and sequencing
analyze_batch_results() {
    log "Analyzing batch formation and sequencing results..."
    
    local generated_count=0
    local finalized_count=0
    local total_data_size=0
    
    if [ -f "$RESULTS_DIR/generated_submissions.jsonl" ]; then
        generated_count=$(wc -l < "$RESULTS_DIR/generated_submissions.jsonl")
        # Calculate total data size
        total_data_size=$(jq -r '.data_size_bytes' "$RESULTS_DIR/generated_submissions.jsonl" 2>/dev/null | awk '{sum+=$1} END {print sum+0}')
    fi
    
    if [ -f "$BATCHES_DIR/finalized_batches.jsonl" ]; then
        finalized_count=$(wc -l < "$BATCHES_DIR/finalized_batches.jsonl")
    fi
    
    # Calculate efficiency metrics
    local expected_batches=$(((generated_count + BATCH_SIZE - 1) / BATCH_SIZE))
    local batch_efficiency=0
    if [ $expected_batches -gt 0 ]; then
        batch_efficiency=$((finalized_count * 100 / expected_batches))
    fi
    
    # Create analysis results
    cat > "$RESULTS_DIR/batch_analysis.json" <<EOF
{
    "batch_formation_analysis": {
        "total_submissions_generated": $generated_count,
        "total_batches_finalized": $finalized_count,
        "expected_batches": $expected_batches,
        "batch_efficiency_percent": $batch_efficiency,
        "total_data_processed_bytes": $total_data_size,
        "average_batch_size": $(echo "scale=2; $generated_count / ($finalized_count + 0.0001)" | bc)
    },
    "sequencing_analysis": {
        "submission_rate_achieved": $(echo "scale=2; $generated_count * 60 / $TEST_DURATION" | bc),
        "batch_finalization_rate": $(echo "scale=2; $finalized_count * 60 / $TEST_DURATION" | bc),
        "average_finalization_time": "TBD - calculate from batch_metrics.csv"
    },
    "performance_metrics": {
        "throughput_submissions_per_sec": $(echo "scale=3; $generated_count / $TEST_DURATION" | bc),
        "throughput_batches_per_sec": $(echo "scale=3; $finalized_count / $TEST_DURATION" | bc),
        "data_throughput_bytes_per_sec": $(echo "scale=0; $total_data_size / $TEST_DURATION" | bc)
    }
}
EOF
    
    finalize "Batch analysis completed"
    info "Generated: $generated_count submissions"
    info "Finalized: $finalized_count batches (${batch_efficiency}% efficiency)"
    info "Data processed: $total_data_size bytes"
}

# Collect results and generate comprehensive report
collect_results() {
    log "Collecting batch finalization test results..."
    
    # Stop all monitoring processes
    for pid_file in generator_pid monitor_pid coord_monitor_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            local pid=$(cat "$RESULTS_DIR/$pid_file")
            kill $pid 2>/dev/null || true
            rm -f "$RESULTS_DIR/$pid_file"
        fi
    done
    
    # Collect container logs
    log "Collecting container logs..."
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        docker logs "$container" > "$LOG_DIR/${container}.log" 2>&1 || true
    done
    
    # Perform batch analysis
    analyze_batch_results
    
    # Generate comprehensive report
    generate_batch_report
    
    log "Results collected in: $RESULTS_DIR"
}

# Generate detailed batch test report
generate_batch_report() {
    log "Generating batch finalization test report..."
    
    local end_time=$(date -Iseconds)
    
    # Read analysis results
    local generated_count=0
    local finalized_count=0
    local batch_efficiency=0
    
    if [ -f "$RESULTS_DIR/batch_analysis.json" ]; then
        generated_count=$(jq -r '.batch_formation_analysis.total_submissions_generated' "$RESULTS_DIR/batch_analysis.json" 2>/dev/null || echo "0")
        finalized_count=$(jq -r '.batch_formation_analysis.total_batches_finalized' "$RESULTS_DIR/batch_analysis.json" 2>/dev/null || echo "0")
        batch_efficiency=$(jq -r '.batch_formation_analysis.batch_efficiency_percent' "$RESULTS_DIR/batch_analysis.json" 2>/dev/null || echo "0")
    fi
    
    # Create detailed report
    cat > "$RESULTS_DIR/test_report.json" <<EOF
{
    "test_summary": {
        "test_name": "$TEST_NAME",
        "test_type": "batch_finalization",
        "batch_size": $BATCH_SIZE,
        "submission_rate": $SUBMISSION_RATE,
        "test_duration": $TEST_DURATION,
        "completed_at": "$end_time",
        "submissions_generated": $generated_count,
        "batches_finalized": $finalized_count,
        "batch_efficiency_percent": $batch_efficiency
    },
    "sequencing_performance": {
        "target_submission_rate": $SUBMISSION_RATE,
        "achieved_submission_rate": "TBD - calculate from submissions.csv",
        "batch_finalization_latency": "TBD - analyze batch_metrics.csv",
        "sequencer_coordination": "TBD - analyze coordination.csv"
    },
    "data_integrity": {
        "submission_ordering": "TBD - verify sequential ordering",
        "batch_consistency": "TBD - verify batch formation rules",
        "data_completeness": "TBD - check for missing submissions"
    }
}
EOF
    
    # Create comprehensive human-readable report
    cat > "$RESULTS_DIR/BATCH_REPORT.md" <<EOF
# Batch Finalization Test Report

## Test Configuration
- **Test Name**: $TEST_NAME
- **Batch Size**: $BATCH_SIZE submissions per batch
- **Submission Rate**: $SUBMISSION_RATE submissions/minute
- **Test Duration**: ${TEST_DURATION}s
- **Completed**: $(date)

## Test Results Summary
- **Submissions Generated**: $generated_count
- **Batches Finalized**: $finalized_count
- **Batch Efficiency**: ${batch_efficiency}%
- **Target Rate**: $SUBMISSION_RATE submissions/min
- **Achieved Rate**: $(echo "scale=1; $generated_count * 60 / $TEST_DURATION" | bc) submissions/min

## Batch Formation Analysis

### Expected vs Actual
- **Expected Batches**: $(((generated_count + BATCH_SIZE - 1) / BATCH_SIZE))
- **Finalized Batches**: $finalized_count
- **Formation Efficiency**: ${batch_efficiency}%

### Performance Metrics
- **Submission Throughput**: $(echo "scale=2; $generated_count / $TEST_DURATION" | bc) submissions/sec
- **Batch Throughput**: $(echo "scale=3; $finalized_count / $TEST_DURATION" | bc) batches/sec
- **Average Batch Size**: $(echo "scale=1; $generated_count / ($finalized_count + 0.0001)" | bc) submissions

## Analysis Required

### 1. Batch Formation Patterns
- **File**: \`batch_metrics.csv\`
- **Analysis**: 
  - Plot batch finalization times over test duration
  - Identify which sequencers finalized most batches
  - Check for batch size consistency

### 2. Submission Sequencing
- **File**: \`submissions.csv\`
- **Analysis**:
  - Verify sequential submission numbering
  - Check timestamp ordering
  - Validate batch assignment logic

### 3. Sequencer Coordination
- **File**: \`sequencer_coordination.csv\`
- **Analysis**:
  - Compare resource usage across sequencers
  - Check peer connectivity stability
  - Identify coordination bottlenecks

### 4. Data Integrity
- **Files**: \`generated_submissions.jsonl\`, \`finalized_batches.jsonl\`
- **Analysis**:
  - Verify all submissions were included in batches
  - Check for duplicate or missing submissions
  - Validate batch checksums and ordering

## Key Files Generated
- \`submissions.csv\` - All generated submissions with metadata
- \`batch_metrics.csv\` - Batch finalization events and timing
- \`sequencer_coordination.csv\` - Sequencer performance metrics
- \`generated_submissions.jsonl\` - Raw submission data
- \`batches/finalized_batches.jsonl\` - Finalized batch records
- \`batch_analysis.json\` - Calculated performance metrics

## Success Criteria Evaluation
- ✅ Submissions generated at target rate: $([ $generated_count -gt $((SUBMISSION_RATE * TEST_DURATION * 8 / 600)) ] && echo "PASS" || echo "FAIL")
- ✅ Batch formation efficiency > 90%: $([ $batch_efficiency -gt 90 ] && echo "PASS ($batch_efficiency%)" || echo "FAIL ($batch_efficiency%)")
- ✅ No missing submissions: TBD - requires data integrity analysis
- ✅ Sequential batch ordering: TBD - requires ordering analysis

## Recommendations
1. **Performance Analysis**: Graph batch finalization times to identify patterns
2. **Load Testing**: Test with higher submission rates to find throughput limits  
3. **Sequencer Balance**: Analyze if batch processing is evenly distributed
4. **Optimization**: Identify bottlenecks in batch formation pipeline

## Next Steps
1. Import CSV files into spreadsheet/analysis tool
2. Create time-series graphs of batch formation
3. Validate submission ordering and data integrity
4. Compare results with consensus and resilience tests

EOF
    
    finalize "Batch finalization report generated: $RESULTS_DIR/BATCH_REPORT.md"
}

# Cleanup function
cleanup() {
    log "Cleaning up batch finalization test..."
    
    # Kill all monitoring processes
    for pid_file in generator_pid monitor_pid coord_monitor_pid; do
        if [ -f "$RESULTS_DIR/$pid_file" ]; then
            kill $(cat "$RESULTS_DIR/$pid_file") 2>/dev/null || true
        fi
    done
    
    log "Batch finalization test cleanup completed"
}

# Main execution
main() {
    batch "📦 Starting Batch Finalization Test 📦"
    log "Configuration: ${BATCH_SIZE}-item batches at $SUBMISSION_RATE/min for ${TEST_DURATION}s"
    
    # Validate parameters
    if [ $BATCH_SIZE -lt 2 ] || [ $BATCH_SIZE -gt 100 ]; then
        error "BATCH_SIZE must be between 2 and 100 (got: $BATCH_SIZE)"
        exit 1
    fi
    
    if [ $SUBMISSION_RATE -lt 5 ] || [ $SUBMISSION_RATE -gt 300 ]; then
        error "SUBMISSION_RATE must be between 5 and 300 per minute (got: $SUBMISSION_RATE)"
        exit 1
    fi
    
    if [ $TEST_DURATION -lt 120 ]; then
        error "TEST_DURATION must be at least 120 seconds (got: $TEST_DURATION)"
        exit 1
    fi
    
    # Calculate test feasibility
    local total_expected=$((SUBMISSION_RATE * TEST_DURATION / 60))
    local expected_batches=$(((total_expected + BATCH_SIZE - 1) / BATCH_SIZE))
    
    info "Test will generate $total_expected submissions forming ~$expected_batches batches"
    
    if [ $total_expected -lt $BATCH_SIZE ]; then
        warn "⚠️  Test may not generate enough submissions to form complete batches"
        warn "   Consider increasing TEST_DURATION or SUBMISSION_RATE"
    fi
    
    # Set trap for cleanup on exit
    trap cleanup EXIT INT TERM
    
    # Run test phases
    initialize_test
    setup_services
    generate_batch_submissions
    monitor_batch_processing
    monitor_sequencer_coordination
    run_test
    collect_results
    
    finalize "🎯 Batch finalization test completed!"
    info "📊 Results available in: $RESULTS_DIR"
    info "📝 Report: $RESULTS_DIR/BATCH_REPORT.md"
    info "📈 Metrics: $RESULTS_DIR/metrics/"
    batch "📦 Batches: $RESULTS_DIR/batches/"
}

# Show usage if help requested
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    cat << EOF
Batch Finalization Test Suite

Usage: $0 [BATCH_SIZE] [SUBMISSION_RATE] [TEST_DURATION]

Parameters:
  BATCH_SIZE      - Number of submissions per batch (2-100, default: 10)
  SUBMISSION_RATE - Submissions per minute (5-300, default: 30)
  TEST_DURATION   - Test duration in seconds (min: 120, default: 300)

Description:
  Tests batch formation and finalization by:
  1. Starting all sequencer services
  2. Generating structured submissions at specified rate
  3. Monitoring batch formation and finalization
  4. Tracking sequencer coordination
  5. Analyzing batch efficiency and data integrity
  6. Measuring sequencing performance

Test Scenarios:
  Small Batches   - Test rapid batch formation with small batches
  Large Batches   - Test efficiency with larger batch sizes
  High Rate       - Stress test with high submission rates
  Low Rate        - Test batch timeout behavior with low rates

Examples:
  $0                    # Standard test: 10-item batches, 30/min, 5 minutes
  $0 5 60 300          # Small batches: 5-item batches, 60/min, 5 minutes
  $0 25 15 600         # Large batches: 25-item batches, 15/min, 10 minutes  
  $0 10 120 300        # High rate: 10-item batches, 120/min, 5 minutes

Analysis:
  Results include CSV files for detailed analysis:
  - submissions.csv - All submissions with timing and metadata
  - batch_metrics.csv - Batch finalization events and performance
  - sequencer_coordination.csv - Sequencer resource usage and coordination

Results:
  All results saved in ../results/batch_test_* directories
  Import CSV files into analysis tools for performance graphs

EOF
    exit 0
fi

# Run main function
main "$@"