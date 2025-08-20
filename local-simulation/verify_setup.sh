#!/bin/bash

# verify_setup.sh - Verify simulation environment setup
# Usage: ./verify_setup.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[VERIFY]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[VERIFY]${NC} $1"
}

error() {
    echo -e "${RED}[VERIFY]${NC} $1"
}

info() {
    echo -e "${BLUE}[VERIFY]${NC} $1"
}

success() {
    echo -e "${GREEN}✅${NC} $1"
}

fail() {
    echo -e "${RED}❌${NC} $1"
}

# Track verification results
VERIFICATION_PASSED=0
VERIFICATION_FAILED=0

verify_item() {
    local description="$1"
    local command="$2"
    
    if eval "$command" >/dev/null 2>&1; then
        success "$description"
        VERIFICATION_PASSED=$((VERIFICATION_PASSED + 1))
        return 0
    else
        fail "$description"
        VERIFICATION_FAILED=$((VERIFICATION_FAILED + 1))
        return 1
    fi
}

# Header
log "Starting P2P Sequencer Simulation Environment Verification"
echo "================================================================"

# System Requirements
info "Checking System Requirements..."
verify_item "Docker is installed and running" "docker info"
verify_item "Docker Compose is available" "docker-compose --version"
verify_item "Basic networking tools available" "which nc && which netstat"
verify_item "jq (JSON processor) is available" "which jq"
verify_item "bc (calculator) is available" "which bc"

# Port Availability
info "Checking Port Availability..."
verify_item "Port 3000 available (Grafana)" "! netstat -tuln | grep -q ':3000 '"
verify_item "Port 8001 available (Sequencer)" "! netstat -tuln | grep -q ':8001 '"
verify_item "Port 9090 available (Prometheus)" "! netstat -tuln | grep -q ':9090 '"
verify_item "Port 9100 available (Bootstrap)" "! netstat -tuln | grep -q ':9100 '"

# File Structure
info "Checking File Structure..."
verify_item "Docker compose file exists" "[ -f 'docker/docker-compose.yml' ]"
verify_item "Main Dockerfile exists" "[ -f 'docker/Dockerfile' ]"
verify_item "Monitoring compose file exists" "[ -f 'monitoring/docker-compose.monitoring.yml' ]"
verify_item "Prometheus config exists" "[ -f 'monitoring/prometheus.yml' ]"
verify_item "Main orchestrator exists" "[ -f 'run_simulation.sh' ]"

# Scripts Verification  
info "Checking Scripts..."
verify_item "Network latency script exists and executable" "[ -x 'scripts/simulate_latency.sh' ]"
verify_item "Packet loss script exists and executable" "[ -x 'scripts/simulate_packet_loss.sh' ]"
verify_item "Partition script exists and executable" "[ -x 'scripts/simulate_partition.sh' ]"
verify_item "Reset network script exists and executable" "[ -x 'scripts/reset_network.sh' ]"

# Test Scripts
info "Checking Test Scripts..."
verify_item "Consensus test script exists and executable" "[ -x 'tests/test_consensus.sh' ]"
verify_item "Byzantine test script exists and executable" "[ -x 'tests/test_byzantine.sh' ]"
verify_item "Resilience test script exists and executable" "[ -x 'tests/test_network_resilience.sh' ]"
verify_item "Batch test script exists and executable" "[ -x 'tests/test_batch_finalization.sh' ]"

# Go Code Compilation
info "Checking Go Code Compilation..."
verify_item "Bootstrap node compiles" "cd ../submissions-bootstrap-node && go build -o test-build ./cmd/main.go && rm -f test-build"
verify_item "Sequencer listener compiles" "cd ../libp2p-submission-sequencer-listener && go build -o test-build ./cmd/main.go && rm -f test-build"
verify_item "P2P debugger compiles" "cd ../p2p-debugger && go build -o test-build . && rm -f test-build"
verify_item "Local collector compiles" "cd ../snapshotter-lite-v2/snapshotter-lite-local-collector && go build -o test-build ./cmd/main.go && rm -f test-build"

# Docker Build Test (optional, takes time)
if [ "$1" = "--docker-build" ]; then
    info "Testing Docker Builds (this may take several minutes)..."
    verify_item "Bootstrap node Docker image builds" "cd docker && docker build --build-arg SERVICE_TYPE=bootstrap -t test-bootstrap . && docker rmi test-bootstrap"
    verify_item "Sequencer node Docker image builds" "cd docker && docker build --build-arg SERVICE_TYPE=sequencer -t test-sequencer . && docker rmi test-sequencer"
    verify_item "Debugger node Docker image builds" "cd docker && docker build --build-arg SERVICE_TYPE=debugger -t test-debugger . && docker rmi test-debugger"
fi

# Directory Structure
info "Checking Directory Structure..."
for dir in results configs logs monitoring/grafana-provisioning; do
    verify_item "Directory '$dir' can be created" "mkdir -p '$dir'"
done

# Disk Space
info "Checking Disk Space..."
available_space=$(df . | awk 'NR==2 {print $4}')
if [ "$available_space" -gt 2097152 ]; then  # 2GB in KB
    success "Adequate disk space available ($(($available_space / 1024 / 1024))GB)"
    VERIFICATION_PASSED=$((VERIFICATION_PASSED + 1))
else
    fail "Insufficient disk space ($(($available_space / 1024))MB available, 2GB recommended)"
    VERIFICATION_FAILED=$((VERIFICATION_FAILED + 1))
fi

# Memory Check
info "Checking Available Memory..."
case "$(uname -s)" in
    "Linux")
        available_memory=$(free -m | awk 'NR==2{print $7}')  # Available memory
        if [ "$available_memory" -gt 4096 ]; then
            success "Adequate memory available (${available_memory}MB)"
            VERIFICATION_PASSED=$((VERIFICATION_PASSED + 1))
        else
            warn "Low available memory (${available_memory}MB, 4GB+ recommended)"
            VERIFICATION_FAILED=$((VERIFICATION_FAILED + 1))
        fi
        ;;
    "Darwin")
        # macOS memory check
        total_memory=$(sysctl -n hw.memsize | awk '{print $1/1024/1024/1024}')
        if [ "${total_memory%.*}" -gt 8 ]; then
            success "Adequate total memory (${total_memory%.*}GB)"
            VERIFICATION_PASSED=$((VERIFICATION_PASSED + 1))
        else
            warn "Limited total memory (${total_memory%.*}GB, 8GB+ recommended)"
            VERIFICATION_FAILED=$((VERIFICATION_FAILED + 1))
        fi
        ;;
    *)
        warn "Memory check not supported on $(uname -s)"
        ;;
esac

# Final Summary
echo ""
echo "================================================================"
log "Verification Summary"
info "Passed: $VERIFICATION_PASSED"
if [ $VERIFICATION_FAILED -gt 0 ]; then
    warn "Failed: $VERIFICATION_FAILED"
else
    info "Failed: $VERIFICATION_FAILED"
fi

# Results
if [ $VERIFICATION_FAILED -eq 0 ]; then
    success "🎉 All verifications passed! Environment is ready for simulation."
    echo ""
    info "Quick start commands:"
    info "  ./run_simulation.sh start          # Start environment"
    info "  ./run_simulation.sh suite basic    # Run basic test suite"
    info "  ./run_simulation.sh status         # Check status"
    echo ""
    exit 0
else
    error "⚠️  $VERIFICATION_FAILED verification(s) failed. Please address issues before running simulations."
    echo ""
    info "Common fixes:"
    info "  - Install missing dependencies (Docker, jq, bc, netstat)"
    info "  - Free up ports (kill processes using required ports)"
    info "  - Ensure adequate disk space and memory"
    info "  - Check Go installation and module dependencies"
    echo ""
    exit 1
fi