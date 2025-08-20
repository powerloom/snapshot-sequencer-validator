# P2P Sequencer Network Local Simulation Environment

A comprehensive testing and simulation framework for the decentralized P2P sequencer network, providing isolated Docker-based testing with network condition simulation, monitoring, and automated test suites.

## 🎯 Overview

This simulation environment allows you to:
- **Test P2P Network Behavior**: Simulate real-world network conditions locally
- **Validate Consensus Mechanisms**: Test batch finalization and sequencer coordination  
- **Assess Byzantine Fault Tolerance**: Simulate malicious nodes and network attacks
- **Monitor Performance**: Real-time metrics with Prometheus and Grafana dashboards
- **Analyze Results**: Comprehensive test reports and data analysis tools

## 🏗️ Architecture

The simulation runs a complete P2P sequencer network in Docker containers:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Simulation Network                           │
├─────────────────────────────────────────────────────────────────┤
│  Bootstrap Node    │  Sequencer-1   │  Sequencer-2   │  Seq-3   │
│  (Discovery)       │  (Consensus)    │  (Consensus)    │ (Cons.) │
│  172.20.0.10:9100  │  172.20.0.11    │  172.20.0.12    │ .13     │
├────────────────────┼─────────────────┼─────────────────┼─────────┤
│  P2P Debugger      │  Network Chaos  │  Monitoring     │         │
│  (Testing)         │  (Simulation)   │  (Metrics)      │         │
│  172.20.0.20       │  172.20.0.100   │  External       │         │
└─────────────────────────────────────────────────────────────────┘
```

## 📋 Prerequisites

- **Docker** 20.0+ with Docker Compose
- **System Requirements**: 8GB RAM, 10GB disk space recommended  
- **Ports**: 3000, 8001-8003, 8080-8083, 9090, 9093, 9100 available
- **Operating System**: Linux, macOS, or Windows with WSL2

### Installation Verification

```bash
# Check Docker
docker --version && docker-compose --version

# Verify available ports
netstat -tulpn | grep -E ":(3000|8001|8002|8003|9090|9100)" || echo "Ports available"

# Check system resources  
free -h  # Linux
top -l 1 | grep PhysMem  # macOS
```

## 🚀 Quick Start

### 1. Start the Simulation Environment

```bash
# Start complete environment (P2P network + monitoring)
./run_simulation.sh start

# Or start without monitoring (faster startup)
MONITORING_ENABLED=false ./run_simulation.sh start
```

### 2. Run Your First Test

```bash
# Run basic consensus test (3 nodes, 5 minutes, 20 submissions/min)
./run_simulation.sh test consensus

# Or run a complete test suite
./run_simulation.sh suite basic
```

### 3. Check Results

```bash
# View environment status
./run_simulation.sh status

# Check logs
./run_simulation.sh logs

# Results are saved in: results/session_*/
```

### 4. Access Monitoring (if enabled)

- **Grafana Dashboard**: http://localhost:3000 (admin/admin123)
- **Prometheus Metrics**: http://localhost:9090
- **AlertManager**: http://localhost:9093

## 📊 Test Types

### Consensus Tests
Validate consensus mechanisms and batch processing:
```bash
./run_simulation.sh test consensus [nodes] [duration] [rate]
./run_simulation.sh test consensus 3 300 20  # 3 nodes, 5min, 20/min
```

### Byzantine Fault Tolerance  
Test resistance to malicious behavior:
```bash
./run_simulation.sh test byzantine [nodes] [fault_type] [duration] 
./run_simulation.sh test byzantine 1 delay 300     # Delay attack
./run_simulation.sh test byzantine 2 partition 600 # Network partition
```

**Fault Types**: `delay`, `partition`, `corrupt`, `crash`, `intermittent`

### Network Resilience
Test behavior under degraded network conditions:
```bash
./run_simulation.sh test resilience [scenario] [intensity] [duration]
./run_simulation.sh test resilience progressive medium 600
./run_simulation.sh test resilience chaos high 900
```

**Scenarios**: `progressive`, `burst`, `chaos`, `recovery`  
**Intensities**: `low`, `medium`, `high`, `extreme`

### Batch Finalization
Test batch creation and sequencing:
```bash
./run_simulation.sh test batch [size] [rate] [duration]
./run_simulation.sh test batch 10 30 300  # 10-item batches, 30/min, 5min
```

## 🧪 Test Suites

### Basic Suite (Default)
```bash
./run_simulation.sh suite basic
```
- Consensus test (3 nodes, 5min)
- Batch finalization test (10-item batches)

### Comprehensive Suite  
```bash
./run_simulation.sh suite comprehensive
```
- All test types with moderate parameters
- ~45 minutes total runtime

### Performance Suite
```bash
./run_simulation.sh suite performance  
```
- High-throughput batch tests
- Extended consensus validation

### Stress Suite
```bash
./run_simulation.sh suite stress
```
- Chaos network conditions
- Byzantine fault combinations

## 🌐 Network Simulation

### Manual Network Conditions

Apply network conditions during testing:

```bash
# Add latency
./scripts/simulate_latency.sh 200 all all          # 200ms to all nodes
./scripts/simulate_latency.sh 500 sequencer-1 all  # 500ms from sequencer-1

# Simulate packet loss  
./scripts/simulate_packet_loss.sh 5 all all        # 5% loss on all nodes
./scripts/simulate_packet_loss.sh --burst 50 sequencer-2 10  # Burst loss

# Create network partitions
./scripts/simulate_partition.sh split              # Split network in half
./scripts/simulate_partition.sh isolate sequencer-1  # Isolate one node
./scripts/simulate_partition.sh bootstrap          # Isolate bootstrap

# Reset all conditions
./scripts/reset_network.sh
```

## 📈 Monitoring and Metrics

### Grafana Dashboards

The monitoring stack provides real-time visibility:

- **Node Status**: Service health and availability
- **P2P Connectivity**: Peer connections and network topology  
- **Message Throughput**: Submission and batch processing rates
- **Performance Metrics**: Latency, CPU, memory usage
- **Network Conditions**: Active simulation conditions

### Key Metrics

- `p2p_peer_count` - Number of connected peers
- `p2p_messages_sent_total` - Messages sent/received
- `p2p_batches_finalized_total` - Batch finalization rate  
- `p2p_message_latency_seconds` - Message delivery latency
- `network_simulation_active` - Active network conditions

### Prometheus Queries

```promql
# Average peer count
avg(p2p_peer_count)

# Message delivery rate (5min)  
rate(p2p_messages_sent_total[5m])

# Batch finalization efficiency
rate(p2p_batches_finalized_total[5m])

# Network health percentage
up / on() group_left count(up) * 100
```

## 📁 Results and Analysis

### Directory Structure

```
results/session_YYYYMMDD_HHMMSS_PID/
├── session_config.json           # Session configuration
├── SESSION_REPORT.md              # Overall session report  
├── consensus_test_HHMMSS/         # Individual test results
│   ├── test_config.json           # Test configuration
│   ├── REPORT.md                  # Test-specific report
│   ├── logs/                      # Container and test logs
│   ├── metrics/                   # CSV data files
│   └── generated_submissions.jsonl # Test data
└── batch_test_HHMMSS/
    ├── batches/finalized_batches.jsonl
    ├── metrics/batch_metrics.csv
    └── ...
```

### CSV Data Files

Import into analysis tools for detailed insights:

- **`submissions.csv`** - All submissions with timestamps and metadata
- **`batch_metrics.csv`** - Batch finalization events and timing
- **`health_metrics.csv`** - Network health over time
- **`sequencer_coordination.csv`** - Sequencer performance metrics

### Sample Analysis

```bash
# View test summary
cat results/session_*/SESSION_REPORT.md

# Analyze batch efficiency
head results/*/batch_test_*/metrics/batch_metrics.csv

# Check network health trends  
tail results/*/resilience_test_*/metrics/health_metrics.csv

# Generate time-series graphs (external tool required)
python analyze_metrics.py results/session_*/metrics/
```

## ⚙️ Configuration

### Environment Variables

```bash
# Disable monitoring stack
MONITORING_ENABLED=false ./run_simulation.sh start

# Keep services running after tests  
CLEANUP_ON_EXIT=false ./run_simulation.sh test consensus

# Custom test parameters
DEFAULT_TEST_DURATION=600 ./run_simulation.sh suite basic
```

### Docker Network Configuration

Edit `docker/docker-compose.yml` to customize:

- Network subnet: `172.20.0.0/16`
- Container IP assignments
- Port mappings
- Resource limits

### Monitoring Configuration  

Edit `monitoring/prometheus.yml` to adjust:
- Scrape intervals
- Retention periods  
- Alert thresholds
- Target endpoints

## 🔧 Troubleshooting

### Common Issues

#### "Port already in use"
```bash
# Find and kill processes using required ports
lsof -ti:3000,8001,9090 | xargs kill -9

# Or use different ports by editing docker-compose.yml
```

#### "Docker build failed"
```bash
# Clean Docker cache
docker system prune -a

# Rebuild with no cache
cd docker && docker-compose build --no-cache
```

#### "Services not becoming healthy"
```bash
# Check service logs
./run_simulation.sh logs

# Verify network connectivity
docker network ls
docker network inspect local-simulation_p2p-simulation
```

#### "Tests failing with network errors"
```bash
# Reset network conditions
./scripts/reset_network.sh --restart

# Verify container connectivity
docker exec p2p-bootstrap ping -c 3 p2p-sequencer-1
```

### Debug Mode

Enable detailed logging:
```bash
# Set log levels
export LOG_LEVEL=DEBUG

# Show all Docker output
docker-compose up --no-daemon

# Monitor real-time logs
docker-compose logs -f
```

### Resource Issues

```bash
# Check Docker resources
docker system df
docker stats

# Free up space
docker system prune -a --volumes

# Adjust container memory limits in docker-compose.yml
```

## 🧩 Advanced Usage

### Custom Test Scenarios

Create custom test combinations:
```bash
# Start environment
./run_simulation.sh start

# Apply network conditions
./scripts/simulate_latency.sh 300 all all

# Run test with custom parameters  
./tests/test_consensus.sh 4 600 50

# Add packet loss during test
sleep 180 && ./scripts/simulate_packet_loss.sh 10 all all

# Collect results and reset
./scripts/reset_network.sh
./run_simulation.sh stop
```

### Integration with CI/CD

```yaml
# .github/workflows/simulation.yml
name: P2P Network Tests
on: [push, pull_request]

jobs:
  simulation:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    - name: Run Simulation Tests
      run: |
        cd local-simulation
        MONITORING_ENABLED=false ./run_simulation.sh suite comprehensive
        
    - name: Upload Results
      uses: actions/upload-artifact@v2
      with:
        name: simulation-results
        path: local-simulation/results/
```

### Performance Benchmarking

```bash
# Baseline performance test
./run_simulation.sh test batch 5 60 300

# High-throughput test  
./run_simulation.sh test batch 20 120 300

# Compare results
diff results/*/BATCH_REPORT.md
```

## 📚 Development

### Adding New Tests

1. Create test script in `tests/`:
```bash
cp tests/test_consensus.sh tests/test_custom.sh
# Edit test_custom.sh
```

2. Add to orchestrator in `run_simulation.sh`:
```bash
# Add case in run_test() function
"custom")
    ./test_custom.sh "${test_args[@]}"
    ;;
```

### Custom Metrics

Add custom metrics to `monitoring/collector_script.sh`:
```bash
# Add metric collection
collect_custom_metrics() {
    echo "custom_metric{label=\"value\"} 42" >> "$metrics_file"
}
```

### Network Condition Scripts

Create new network simulation in `scripts/`:
```bash
cp scripts/simulate_latency.sh scripts/simulate_custom.sh
# Implement custom network conditions
```

## 🤝 Contributing

1. Fork the repository
2. Create feature branch: `git checkout -b feature/new-test`
3. Test your changes: `./run_simulation.sh suite basic`
4. Submit pull request

### Testing Guidelines

- All new tests should include comprehensive error handling
- Test scripts should be idempotent and clean up properly
- Include detailed logging and progress indicators
- Generate machine-readable CSV output for analysis
- Document test parameters and expected outcomes

## 📄 License

This simulation framework is part of the P2P Sequencer project and follows the same license terms.

## 🆘 Support

- **Documentation**: See individual test script help: `./tests/test_*.sh --help`
- **Issues**: Create GitHub issues for bugs or feature requests
- **Monitoring**: Check Grafana dashboards for system health
- **Logs**: All test output is captured in `results/session_*/`

## 📋 Command Reference

### Main Commands
```bash
./run_simulation.sh start                    # Start environment
./run_simulation.sh stop                     # Stop environment  
./run_simulation.sh restart                  # Restart environment
./run_simulation.sh status                   # Check status
./run_simulation.sh clean                    # Clean all resources
./run_simulation.sh logs [service]           # View logs
```

### Test Commands
```bash  
./run_simulation.sh test consensus [nodes] [duration] [rate]
./run_simulation.sh test byzantine [nodes] [fault] [duration]  
./run_simulation.sh test resilience [scenario] [intensity] [duration]
./run_simulation.sh test batch [size] [rate] [duration]
./run_simulation.sh suite [basic|comprehensive|performance|stress]
```

### Network Simulation
```bash
./scripts/simulate_latency.sh [ms] [target] [source]
./scripts/simulate_packet_loss.sh [%] [target] [source]
./scripts/simulate_partition.sh [type] [nodes...]
./scripts/reset_network.sh [--restart]
```

---

**Ready to simulate?** Start with: `./run_simulation.sh start && ./run_simulation.sh suite basic`