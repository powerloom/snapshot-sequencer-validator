#!/bin/bash

# run_simulation.sh - Main orchestrator for P2P sequencer network simulation
# Usage: ./run_simulation.sh [COMMAND] [OPTIONS...]

set -e

# Configuration
DEFAULT_TEST_DURATION=300
DEFAULT_NODE_COUNT=3
DEFAULT_MONITORING=true

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[SIMULATION]${NC} $(date '+%H:%M:%S') $1"
}

warn() {
    echo -e "${YELLOW}[SIMULATION]${NC} $(date '+%H:%M:%S') $1"
}

error() {
    echo -e "${RED}[SIMULATION]${NC} $(date '+%H:%M:%S') $1"
}

info() {
    echo -e "${BLUE}[SIMULATION]${NC} $(date '+%H:%M:%S') $1"
}

success() {
    echo -e "${PURPLE}[SIMULATION]${NC} $(date '+%H:%M:%S') ✅ $1"
}

header() {
    echo -e "${CYAN}[SIMULATION]${NC} $(date '+%H:%M:%S') 🚀 $1"
}

# Global variables
COMMAND=""
MONITORING_ENABLED=true
CLEANUP_ON_EXIT=true
SIMULATION_SESSION_ID=""
RESULTS_BASE_DIR=""

# Generate unique session ID
generate_session_id() {
    SIMULATION_SESSION_ID="sim_$(date +%Y%m%d_%H%M%S)_$$"
    RESULTS_BASE_DIR="results/session_$SIMULATION_SESSION_ID"
    mkdir -p "$RESULTS_BASE_DIR"
    
    log "Session ID: $SIMULATION_SESSION_ID"
    log "Results directory: $RESULTS_BASE_DIR"
}

# Setup environment
setup_environment() {
    log "Setting up simulation environment"
    
    # Create necessary directories
    mkdir -p results configs logs
    
    # Verify Docker is running
    if ! docker info >/dev/null 2>&1; then
        error "Docker is not running. Please start Docker and try again."
        exit 1
    fi
    
    # Verify Docker Compose is available
    if ! docker-compose --version >/dev/null 2>&1; then
        error "Docker Compose is not available. Please install Docker Compose."
        exit 1
    fi
    
    # Check available disk space
    local available_space=$(df . | awk 'NR==2 {print $4}')
    if [ "$available_space" -lt 1048576 ]; then  # Less than 1GB
        warn "Low disk space available ($available_space KB). Simulation may fail."
    fi
    
    # Create session configuration
    cat > "$RESULTS_BASE_DIR/session_config.json" <<EOF
{
    "session_id": "$SIMULATION_SESSION_ID",
    "started_at": "$(date -Iseconds)",
    "command": "$COMMAND",
    "monitoring_enabled": $MONITORING_ENABLED,
    "docker_info": {
        "version": "$(docker --version | cut -d' ' -f3 | cut -d',' -f1)",
        "compose_version": "$(docker-compose --version | cut -d' ' -f3 | cut -d',' -f1)"
    },
    "system_info": {
        "os": "$(uname -s)",
        "arch": "$(uname -m)",
        "available_space_kb": $available_space
    }
}
EOF
    
    success "Environment setup complete"
}

# Start monitoring stack
start_monitoring() {
    if [ "$MONITORING_ENABLED" != "true" ]; then
        info "Monitoring disabled, skipping monitoring stack"
        return 0
    fi
    
    log "Starting monitoring stack"
    
    cd monitoring
    
    # Start monitoring services
    docker-compose -f docker-compose.monitoring.yml up -d
    
    # Wait for services to be ready
    log "Waiting for monitoring services to be ready..."
    local max_wait=60
    local wait_time=0
    
    while [ $wait_time -lt $max_wait ]; do
        if curl -s http://localhost:9090/-/ready >/dev/null 2>&1 && \
           curl -s http://localhost:3000/api/health >/dev/null 2>&1; then
            success "Monitoring services are ready"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Waiting for monitoring services... (${wait_time}s)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Monitoring services may not be fully ready"
    fi
    
    cd - >/dev/null
    
    log "Monitoring stack started:"
    info "  - Prometheus: http://localhost:9090"
    info "  - Grafana: http://localhost:3000 (admin/admin123)"
    info "  - AlertManager: http://localhost:9093"
}

# Stop monitoring stack
stop_monitoring() {
    if [ "$MONITORING_ENABLED" != "true" ]; then
        return 0
    fi
    
    log "Stopping monitoring stack"
    
    cd monitoring
    docker-compose -f docker-compose.monitoring.yml down
    cd - >/dev/null
}

# Start P2P network
start_network() {
    log "Starting P2P sequencer network"
    
    cd docker
    
    # Build services
    log "Building Docker images..."
    docker-compose build --parallel
    
    # Start network services
    log "Starting network services..."
    docker-compose up -d
    
    # Wait for network to be healthy
    log "Waiting for network services to be healthy..."
    local max_wait=120
    local wait_time=0
    
    while [ $wait_time -lt $max_wait ]; do
        local healthy_count=$(docker-compose ps | grep "Up" | wc -l)
        if [ $healthy_count -ge 5 ]; then
            success "All network services are healthy"
            break
        fi
        
        sleep 5
        wait_time=$((wait_time + 5))
        info "Network health check: $healthy_count/5 services (${wait_time}s)"
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "Not all network services became healthy within ${max_wait}s"
        docker-compose ps
    fi
    
    cd - >/dev/null
    
    log "P2P network started successfully"
}

# Stop P2P network
stop_network() {
    log "Stopping P2P sequencer network"
    
    cd docker
    docker-compose down
    cd - >/dev/null
    
    # Reset any network conditions
    scripts/reset_network.sh >/dev/null 2>&1 || true
}

# Run a specific test
run_test() {
    local test_type=$1
    shift
    local test_args=("$@")
    
    log "Running test: $test_type"
    info "Test arguments: ${test_args[*]}"
    
    # Create test-specific results directory
    local test_results_dir="$RESULTS_BASE_DIR/${test_type}_$(date +%H%M%S)"
    mkdir -p "$test_results_dir"
    
    cd tests
    
    case "$test_type" in
        "consensus")
            ./test_consensus.sh "${test_args[@]}" 2>&1 | tee "$test_results_dir/test_output.log"
            ;;
        "byzantine")
            ./test_byzantine.sh "${test_args[@]}" 2>&1 | tee "$test_results_dir/test_output.log"
            ;;
        "resilience")
            ./test_network_resilience.sh "${test_args[@]}" 2>&1 | tee "$test_results_dir/test_output.log"
            ;;
        "batch")
            ./test_batch_finalization.sh "${test_args[@]}" 2>&1 | tee "$test_results_dir/test_output.log"
            ;;
        *)
            error "Unknown test type: $test_type"
            return 1
            ;;
    esac
    
    cd - >/dev/null
    
    success "Test $test_type completed"
    info "Test output: $test_results_dir/test_output.log"
}

# Run test suite
run_test_suite() {
    local suite_type=${1:-"basic"}
    
    header "Running test suite: $suite_type"
    
    case "$suite_type" in
        "basic")
            log "Running basic test suite (consensus + batch)"
            run_test "consensus" 3 300 20
            sleep 30  # Brief pause between tests
            run_test "batch" 10 30 300
            ;;
        "comprehensive")
            log "Running comprehensive test suite (all tests)"
            run_test "consensus" 3 300 20
            sleep 60
            run_test "batch" 10 30 300
            sleep 60
            run_test "resilience" "progressive" "medium" 600
            sleep 60
            run_test "byzantine" 1 "delay" 300
            ;;
        "performance")
            log "Running performance test suite"
            run_test "batch" 20 60 600    # Large batches, high rate
            sleep 60
            run_test "consensus" 3 600 40  # Extended consensus test
            ;;
        "stress")
            log "Running stress test suite"
            run_test "resilience" "chaos" "high" 900
            sleep 60
            run_test "byzantine" 2 "intermittent" 600
            ;;
        *)
            error "Unknown test suite: $suite_type"
            return 1
            ;;
    esac
    
    success "Test suite $suite_type completed"
}

# Generate comprehensive report
generate_report() {
    log "Generating simulation session report"
    
    local report_file="$RESULTS_BASE_DIR/SESSION_REPORT.md"
    local end_time=$(date -Iseconds)
    
    # Count test results
    local test_count=$(find "$RESULTS_BASE_DIR" -name "*test*" -type d | wc -l)
    local log_count=$(find "$RESULTS_BASE_DIR" -name "*.log" | wc -l)
    local results_size=$(du -sh "$RESULTS_BASE_DIR" | cut -f1)
    
    cat > "$report_file" <<EOF
# P2P Sequencer Simulation Session Report

## Session Summary
- **Session ID**: $SIMULATION_SESSION_ID
- **Started**: $(jq -r '.started_at' "$RESULTS_BASE_DIR/session_config.json" 2>/dev/null || echo "Unknown")
- **Completed**: $end_time
- **Command**: $COMMAND
- **Monitoring**: $([ "$MONITORING_ENABLED" = "true" ] && echo "Enabled" || echo "Disabled")

## Results Overview
- **Tests Executed**: $test_count
- **Log Files Generated**: $log_count
- **Total Results Size**: $results_size

## Test Results
$(find "$RESULTS_BASE_DIR" -name "*test*" -type d | while read test_dir; do
    test_name=$(basename "$test_dir")
    if [ -f "$test_dir/test_output.log" ]; then
        echo "### $test_name"
        echo "- **Directory**: \`$test_dir\`"
        echo "- **Output Log**: \`$test_dir/test_output.log\`"
        
        # Extract test status from log
        if grep -q "completed successfully" "$test_dir/test_output.log" 2>/dev/null; then
            echo "- **Status**: ✅ Completed Successfully"
        elif grep -q "completed" "$test_dir/test_output.log" 2>/dev/null; then
            echo "- **Status**: ⚠️ Completed with Warnings"
        else
            echo "- **Status**: ❌ Failed or Incomplete"
        fi
        echo ""
    fi
done)

## System Information
- **Docker Version**: $(jq -r '.docker_info.version' "$RESULTS_BASE_DIR/session_config.json" 2>/dev/null || echo "Unknown")
- **Docker Compose Version**: $(jq -r '.docker_info.compose_version' "$RESULTS_BASE_DIR/session_config.json" 2>/dev/null || echo "Unknown")
- **OS**: $(jq -r '.system_info.os' "$RESULTS_BASE_DIR/session_config.json" 2>/dev/null || echo "Unknown")
- **Architecture**: $(jq -r '.system_info.arch' "$RESULTS_BASE_DIR/session_config.json" 2>/dev/null || echo "Unknown")

## Monitoring Data
$([ "$MONITORING_ENABLED" = "true" ] && echo "
- **Prometheus**: http://localhost:9090
- **Grafana**: http://localhost:3000
- **AlertManager**: http://localhost:9093

Monitoring data is available while the monitoring stack is running.
" || echo "Monitoring was disabled for this session.")

## Files and Directories
\`\`\`
$(find "$RESULTS_BASE_DIR" -type f -name "*.log" -o -name "*.json" -o -name "*.csv" -o -name "*.md" | sort)
\`\`\`

## Next Steps
1. Review individual test reports in their respective directories
2. Analyze metrics and logs for performance insights
3. Use monitoring dashboards for visual analysis (if monitoring enabled)
4. Archive or clean up results as needed

---
*Generated by P2P Sequencer Simulation Framework*
*Session: $SIMULATION_SESSION_ID*
EOF
    
    success "Session report generated: $report_file"
    info "Results directory: $RESULTS_BASE_DIR"
}

# Clean up resources
cleanup() {
    log "Starting cleanup process"
    
    if [ "$CLEANUP_ON_EXIT" = "true" ]; then
        # Stop network services
        stop_network 2>/dev/null || true
        
        # Stop monitoring if enabled
        stop_monitoring 2>/dev/null || true
        
        # Reset network conditions
        scripts/reset_network.sh >/dev/null 2>&1 || true
    else
        warn "Cleanup disabled - services left running"
        info "To stop services manually:"
        info "  - P2P Network: cd docker && docker-compose down"
        [ "$MONITORING_ENABLED" = "true" ] && info "  - Monitoring: cd monitoring && docker-compose -f docker-compose.monitoring.yml down"
    fi
    
    # Generate final report
    generate_report
    
    log "Cleanup completed"
}

# Show service status
show_status() {
    header "Simulation Environment Status"
    
    # Check Docker
    if docker info >/dev/null 2>&1; then
        success "Docker is running"
    else
        error "Docker is not running"
    fi
    
    # Check P2P network services
    info "P2P Network Services:"
    cd docker
    if docker-compose ps | grep -q "Up"; then
        local healthy_count=$(docker-compose ps | grep "Up" | wc -l)
        success "  $healthy_count services running"
        docker-compose ps
    else
        warn "  No P2P services running"
    fi
    cd - >/dev/null
    
    # Check monitoring services
    if [ "$MONITORING_ENABLED" = "true" ]; then
        info "Monitoring Services:"
        cd monitoring
        if docker-compose -f docker-compose.monitoring.yml ps | grep -q "Up"; then
            local monitor_count=$(docker-compose -f docker-compose.monitoring.yml ps | grep "Up" | wc -l)
            success "  $monitor_count monitoring services running"
        else
            warn "  No monitoring services running"
        fi
        cd - >/dev/null
    fi
    
    # Check available space
    local available_space=$(df . | awk 'NR==2 {print $4}')
    info "Available disk space: $(($available_space / 1024)) MB"
    
    # Check recent results
    if [ -d "results" ]; then
        local result_count=$(find results -name "*test*" -type d -newerct '1 day ago' | wc -l)
        info "Recent test results (24h): $result_count"
    fi
}

# Main execution logic
main() {
    COMMAND="$1"
    shift || true
    
    case "$COMMAND" in
        "start")
            header "Starting P2P Sequencer Simulation Environment"
            generate_session_id
            setup_environment
            start_monitoring
            start_network
            success "Simulation environment started"
            info "Use './run_simulation.sh test [TYPE]' to run tests"
            info "Use './run_simulation.sh status' to check status"
            ;;
            
        "stop")
            header "Stopping P2P Sequencer Simulation Environment"
            stop_network
            stop_monitoring
            success "Simulation environment stopped"
            ;;
            
        "restart")
            header "Restarting P2P Sequencer Simulation Environment"
            stop_network
            stop_monitoring
            sleep 5
            generate_session_id
            setup_environment
            start_monitoring
            start_network
            success "Simulation environment restarted"
            ;;
            
        "test")
            local test_type=$1
            shift || true
            if [ -z "$test_type" ]; then
                error "Test type required. Options: consensus, byzantine, resilience, batch"
                exit 1
            fi
            generate_session_id
            setup_environment
            trap cleanup EXIT INT TERM
            run_test "$test_type" "$@"
            ;;
            
        "suite")
            local suite_type=${1:-"basic"}
            generate_session_id
            setup_environment
            trap cleanup EXIT INT TERM
            start_monitoring
            start_network
            run_test_suite "$suite_type"
            ;;
            
        "status")
            show_status
            ;;
            
        "clean")
            header "Cleaning up all simulation resources"
            stop_network
            stop_monitoring
            scripts/reset_network.sh >/dev/null 2>&1 || true
            docker system prune -f >/dev/null 2>&1 || true
            success "Cleanup completed"
            ;;
            
        "logs")
            local service=${1:-"all"}
            if [ "$service" = "all" ]; then
                info "All service logs:"
                cd docker && docker-compose logs --tail=50
            else
                info "Logs for $service:"
                cd docker && docker-compose logs --tail=50 "$service"
            fi
            ;;
            
        *)
            show_usage
            exit 1
            ;;
    esac
}

# Show usage information
show_usage() {
    cat << EOF
P2P Sequencer Network Simulation Framework

Usage: $0 COMMAND [OPTIONS...]

Commands:
  start                    - Start simulation environment (network + monitoring)
  stop                     - Stop simulation environment  
  restart                  - Restart simulation environment
  test TYPE [ARGS...]      - Run specific test type
  suite [TYPE]             - Run test suite
  status                   - Show environment status
  clean                    - Clean up all resources
  logs [SERVICE]           - Show service logs

Test Types:
  consensus [nodes] [duration] [rate]     - Test consensus mechanism
  byzantine [nodes] [fault] [duration]    - Test Byzantine fault tolerance
  resilience [scenario] [intensity] [dur] - Test network resilience
  batch [size] [rate] [duration]          - Test batch finalization

Test Suites:
  basic         - Consensus + batch tests (default)
  comprehensive - All test types
  performance   - Performance-focused tests
  stress        - High-intensity stress tests

Examples:
  $0 start                                 # Start environment
  $0 test consensus 3 300 20              # Test consensus with 3 nodes
  $0 test byzantine 1 delay 300           # Test Byzantine delay faults
  $0 suite comprehensive                  # Run all tests
  $0 status                               # Check status
  $0 stop                                 # Stop environment

Environment Variables:
  MONITORING_ENABLED=false                # Disable monitoring stack
  CLEANUP_ON_EXIT=false                   # Keep services running after tests

Results:
  All test results are saved in results/session_* directories
  Each test generates detailed logs and analysis reports

Monitoring (when enabled):
  - Prometheus: http://localhost:9090
  - Grafana: http://localhost:3000 (admin/admin123)
  - AlertManager: http://localhost:9093

EOF
}

# Handle environment variables
[ -n "$MONITORING_ENABLED" ] && MONITORING_ENABLED="$MONITORING_ENABLED"
[ -n "$CLEANUP_ON_EXIT" ] && CLEANUP_ON_EXIT="$CLEANUP_ON_EXIT"

# Trap for cleanup on script exit
trap cleanup EXIT INT TERM

# Run main function
main "$@"