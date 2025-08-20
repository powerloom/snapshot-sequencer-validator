#!/bin/sh

# Custom metrics collector for P2P simulation data
# Collects metrics from test results and exposes them for Prometheus

set -e

PROMETHEUS_GATEWAY=${PROMETHEUS_GATEWAY:-"prometheus:9090"}
RESULTS_DIR=${RESULTS_DIR:-"/results"}
COLLECTION_INTERVAL=${COLLECTION_INTERVAL:-15}
METRICS_PORT=${METRICS_PORT:-8090}

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] COLLECTOR: $1"
}

# Setup HTTP server for metrics endpoint
setup_metrics_server() {
    log "Starting metrics HTTP server on port $METRICS_PORT"
    
    # Create metrics directory
    mkdir -p /tmp/metrics
    
    # Start simple HTTP server in background
    (
        cd /tmp/metrics
        while true; do
            echo -e "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n$(cat metrics.txt 2>/dev/null || echo '# No metrics yet')" | nc -l -p $METRICS_PORT
        done
    ) &
    
    log "Metrics server started on port $METRICS_PORT"
}

# Collect test results metrics
collect_test_metrics() {
    local metrics_file="/tmp/metrics/metrics.txt"
    
    # Initialize metrics file
    cat > "$metrics_file" <<EOF
# HELP p2p_simulation_test_active Indicates if a simulation test is currently active
# TYPE p2p_simulation_test_active gauge
# HELP p2p_simulation_submissions_total Total number of submissions generated in tests
# TYPE p2p_simulation_submissions_total counter
# HELP p2p_simulation_batches_total Total number of batches finalized in tests
# TYPE p2p_simulation_batches_total counter
# HELP p2p_simulation_test_duration_seconds Duration of current/last test
# TYPE p2p_simulation_test_duration_seconds gauge
EOF

    # Check for active tests
    local active_tests=0
    local total_submissions=0
    local total_batches=0
    local test_duration=0
    
    if [ -d "$RESULTS_DIR" ]; then
        # Count active test directories (created in last hour)
        active_tests=$(find "$RESULTS_DIR" -maxdepth 1 -type d -name "*test*" -newerct '1 hour ago' | wc -l)
        
        # Find most recent test directory
        local latest_test=$(find "$RESULTS_DIR" -maxdepth 1 -type d -name "*test*" -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)
        
        if [ -n "$latest_test" ] && [ -d "$latest_test" ]; then
            log "Collecting metrics from: $latest_test"
            
            # Count submissions from various test files
            if [ -f "$latest_test/generated_submissions.jsonl" ]; then
                local submissions=$(wc -l < "$latest_test/generated_submissions.jsonl")
                total_submissions=$submissions
            fi
            
            if [ -f "$latest_test/submissions.jsonl" ]; then
                local submissions=$(wc -l < "$latest_test/submissions.jsonl")
                total_submissions=$((total_submissions + submissions))
            fi
            
            # Count batches
            if [ -d "$latest_test/batches" ] && [ -f "$latest_test/batches/finalized_batches.jsonl" ]; then
                local batches=$(wc -l < "$latest_test/batches/finalized_batches.jsonl")
                total_batches=$batches
            fi
            
            # Get test duration from config
            if [ -f "$latest_test/test_config.json" ]; then
                test_duration=$(jq -r '.test_duration // 0' "$latest_test/test_config.json" 2>/dev/null || echo "0")
            fi
            
            # Check if test is still running
            if [ -f "$latest_test/generator_pid" ] || [ -f "$latest_test/monitor_pid" ]; then
                active_tests=1
            else
                active_tests=0
            fi
        fi
    fi
    
    # Write metrics
    cat >> "$metrics_file" <<EOF

p2p_simulation_test_active $active_tests
p2p_simulation_submissions_total $total_submissions
p2p_simulation_batches_total $total_batches
p2p_simulation_test_duration_seconds $test_duration

EOF

    # Add network simulation status
    echo "# HELP network_simulation_active Indicates active network simulation conditions" >> "$metrics_file"
    echo "# TYPE network_simulation_active gauge" >> "$metrics_file"
    
    # Check for active network conditions by examining script PIDs
    local latency_active=0
    local packet_loss_active=0
    local partition_active=0
    
    # These would be more sophisticated in a real implementation
    # For now, we'll check for common indicators
    if pgrep -f "simulate_latency" > /dev/null 2>&1; then
        latency_active=1
    fi
    
    if pgrep -f "simulate_packet_loss" > /dev/null 2>&1; then
        packet_loss_active=1
    fi
    
    if pgrep -f "simulate_partition" > /dev/null 2>&1; then
        partition_active=1
    fi
    
    cat >> "$metrics_file" <<EOF
network_simulation_active{condition_type="latency"} $latency_active
network_simulation_active{condition_type="packet_loss"} $packet_loss_active
network_simulation_active{condition_type="partition"} $partition_active

EOF

    log "Metrics updated: tests=$active_tests, submissions=$total_submissions, batches=$total_batches"
}

# Collect container health metrics
collect_health_metrics() {
    local metrics_file="/tmp/metrics/metrics.txt"
    
    echo "" >> "$metrics_file"
    echo "# HELP p2p_container_health Container health status (1=healthy, 0=unhealthy)" >> "$metrics_file"
    echo "# TYPE p2p_container_health gauge" >> "$metrics_file"
    
    # Check health of P2P containers
    for container in p2p-bootstrap p2p-sequencer-1 p2p-sequencer-2 p2p-sequencer-3 p2p-debugger; do
        local health=0
        if docker exec "$container" echo "test" >/dev/null 2>&1; then
            health=1
        fi
        
        echo "p2p_container_health{container=\"$container\"} $health" >> "$metrics_file"
    done
}

# Collect CSV-based metrics from test results
collect_csv_metrics() {
    local metrics_file="/tmp/metrics/metrics.txt"
    
    if [ -d "$RESULTS_DIR" ]; then
        # Find latest test with CSV files
        local latest_test=$(find "$RESULTS_DIR" -maxdepth 1 -type d -name "*test*" -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)
        
        if [ -n "$latest_test" ] && [ -d "$latest_test/metrics" ]; then
            log "Processing CSV metrics from: $latest_test/metrics"
            
            echo "" >> "$metrics_file"
            echo "# CSV-derived metrics" >> "$metrics_file"
            
            # Process health metrics if available
            if [ -f "$latest_test/metrics/health_metrics.csv" ]; then
                local avg_health=$(tail -n 10 "$latest_test/metrics/health_metrics.csv" | awk -F',' 'NR>1{sum+=$3; count++} END{if(count>0) print sum/count; else print 0}')
                echo "# HELP p2p_average_health_percent Average network health percentage" >> "$metrics_file"
                echo "# TYPE p2p_average_health_percent gauge" >> "$metrics_file"
                echo "p2p_average_health_percent $avg_health" >> "$metrics_file"
            fi
            
            # Process batch metrics if available
            if [ -f "$latest_test/metrics/batch_metrics.csv" ]; then
                local batch_count=$(tail -n +2 "$latest_test/metrics/batch_metrics.csv" | wc -l)
                echo "# HELP p2p_csv_batch_count Number of batches recorded in CSV" >> "$metrics_file"
                echo "# TYPE p2p_csv_batch_count gauge" >> "$metrics_file"
                echo "p2p_csv_batch_count $batch_count" >> "$metrics_file"
            fi
        fi
    fi
}

# Main collection loop
main() {
    log "Starting metrics collector"
    log "Prometheus: $PROMETHEUS_GATEWAY"
    log "Results dir: $RESULTS_DIR"
    log "Collection interval: ${COLLECTION_INTERVAL}s"
    
    # Setup metrics server
    setup_metrics_server
    
    # Main collection loop
    while true; do
        log "Collecting metrics..."
        
        collect_test_metrics
        collect_health_metrics
        collect_csv_metrics
        
        log "Metrics collection complete, sleeping ${COLLECTION_INTERVAL}s"
        sleep "$COLLECTION_INTERVAL"
    done
}

# Handle shutdown gracefully
cleanup() {
    log "Shutting down metrics collector"
    pkill -P $$ || true
    exit 0
}

trap cleanup SIGTERM SIGINT

# Start main function
main "$@"