# VPS Deployment Guide for Powerloom Decentralized Sequencer Validator

> [!IMPORTANT]
> This guide covers deployment of the decentralized sequencer validator. Always ensure you're using the latest version and have reviewed the configuration requirements.

## Table of Contents
- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Launch Scripts Reference](#launch-scripts-reference)
- [Monitoring Tools](#monitoring-tools)
- [Deployment Modes](#deployment-modes)
- [Multi-VPS Setup](#multi-vps-setup)
- [Troubleshooting](#troubleshooting)
- [Appendix](#appendix)

## Overview

The Powerloom Decentralized Sequencer Validator provides two deployment systems:

### System 1: Consensus Test (STABLE)
- **Binary**: `cmd/sequencer-consensus-test/main.go`
- **Docker**: `docker-compose.yml`
- **Launcher**: `start.sh`
- **Purpose**: Tests consensus with real P2P listener, dequeuer, but dummy batch generation
- **Status**: Tested and stable for consensus testing

### System 2: Full Decentralized Sequencer (EXPERIMENTAL)  
- **Binary**: `cmd/unified/main.go`
- **Docker**: `docker-compose.snapshot-sequencer.yml` or `docker-compose.distributed.yml`
- **Launcher**: `launch.sh`
- **Purpose**: Production-ready snapshot sequencer with component toggles
- **Status**: Experimental, actively developed

## Prerequisites

### System Requirements
- Ubuntu 20.04+ or compatible Linux distribution
- Docker and Docker Compose installed
- Go 1.24.5+ (for building from source)
- 2GB+ RAM, 10GB+ disk space
- Open ports: 9001 (P2P), 9090 (metrics, optional)

### Generate P2P Identity
```bash
# Generate unique key for your validator
cd key_generator
go run generate_key.go
# Save the output hex private key and peer ID
```

## Quick Start

### 1. Clone and Setup
```bash
# Clone repository
git clone https://github.com/powerloom/snapshot-sequencer-validator.git
cd snapshot-sequencer-validator

# Create configuration from example
cp .env.example .env

# Edit configuration
nano .env
```

### 2. Configure Essential Variables
```bash
# Required P2P settings
BOOTSTRAP_MULTIADDR=/ip4/<BOOTSTRAP_NODE_IPV4_ADDR>/tcp/<PORT>/p2p/<PEER_ID>
PRIVATE_KEY=<your-generated-private-key>

# Required for production
POWERLOOM_RPC_NODES=http://your-rpc-endpoint:8545
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c
```

### 3. Launch Sequencer
```bash
# Quick start with default settings
./launch.sh unified

# Or distributed mode for production
./launch.sh distributed
```

## Configuration

### Environment Variables Reference

#### Core Settings
```bash
# Unique identifier for this sequencer instance
SEQUENCER_ID=unified-sequencer-1

# P2P networking port
P2P_PORT=9001

# Redis configuration (required for queueing)
REDIS_HOST=redis         # 'redis' for Docker, 'localhost' for binary
REDIS_PORT=6379
REDIS_DB=0
REDIS_PASSWORD=          # Leave empty if no auth
```

#### Component Toggles
```bash
# Control which components run (for unified mode)
ENABLE_LISTENER=true      # P2P gossipsub listener
ENABLE_DEQUEUER=true      # Redis queue processor
ENABLE_FINALIZER=false    # Batch finalizer
ENABLE_CONSENSUS=false    # Consensus voting
ENABLE_EVENT_MONITOR=false # EpochReleased event monitoring
```

#### RPC Configuration
```bash
# Powerloom Protocol Chain RPC
# Option 1: Comma-separated (RECOMMENDED)
POWERLOOM_RPC_NODES=http://rpc1.com:8545,http://rpc2.com:8545

# Option 2: JSON array (requires proper quoting)
POWERLOOM_RPC_NODES='["http://rpc1.com:8545","http://rpc2.com:8545"]'

# Archive nodes (optional)
POWERLOOM_ARCHIVE_RPC_NODES=
```

#### Contract Configuration
```bash
# Protocol State Contract (manages epochs)
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF

# Contract ABI path (for event parsing)
CONTRACT_ABI_PATH=./abi/ProtocolContract.json

# Data Market Addresses
# Option 1: Comma-separated (RECOMMENDED)
DATA_MARKET_ADDRESSES=0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c,0x21cb57C1f2352ad215a463DD867b838749CD3b8f

# Option 2: JSON array
DATA_MARKET_ADDRESSES='["0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"]'
```

#### Performance Tuning
```bash
# Dequeuer settings
DEQUEUER_WORKERS=5
DEQUEUER_REPLICAS=3      # For distributed mode
MAX_SUBMISSIONS_PER_EPOCH=100

# Submission windows
SUBMISSION_WINDOW_DURATION=60
MAX_CONCURRENT_WINDOWS=100

# Event monitoring
EVENT_POLL_INTERVAL=12
EVENT_START_BLOCK=0      # 0 = start from current block (recommended)
EVENT_BLOCK_BATCH_SIZE=1000

# Deduplication
DEDUP_ENABLED=true
DEDUP_LOCAL_CACHE_SIZE=10000
DEDUP_TTL_SECONDS=7200
```

## Launch Scripts Reference

### launch.sh - Main Deployment Script

The `launch.sh` script is the primary tool for managing your sequencer deployment.

#### Available Commands

```bash
# Start unified sequencer (all components in one container)
./launch.sh unified

# Start distributed mode (separate containers per component)
./launch.sh distributed

# Start distributed mode with Redis exposed for debugging
./launch.sh distributed-debug

# Start with custom .env settings
./launch.sh sequencer-custom

# Stop all running services
./launch.sh stop

# Clean all containers and volumes (with confirmation)
./launch.sh clean

# Force clean without confirmation
./launch.sh clean --force

# Monitor batch preparation status
./launch.sh monitor

# View usage help
./launch.sh help
```

#### Command Details

**unified**: Launches single container with all components enabled based on .env toggles
```bash
./launch.sh unified
# Uses: docker-compose.snapshot-sequencer.yml
# Service: sequencer-all
```

**distributed**: Production mode with separate containers for each component
```bash
./launch.sh distributed
# Uses: docker-compose.distributed.yml
# Services: listener, dequeuer, event-monitor, finalizer, consensus
# Components scale based on REPLICAS variables
```

**distributed-debug**: Like distributed but with Redis port exposed
```bash
./launch.sh distributed-debug
# Uses: docker-compose.distributed.yml + docker-compose.debug.yml
# Exposes Redis on port 6379 for external monitoring
```

**sequencer-custom**: Uses your .env settings exactly as configured
```bash
./launch.sh sequencer-custom
# Uses: docker-compose.snapshot-sequencer.yml
# Service: sequencer-custom
# Reads all settings from .env
```

**monitor**: Checks batch preparation status
```bash
./launch.sh monitor [container-name]
# Executes monitoring script inside specified or auto-detected container
# Shows: submission windows, ready batches, queue depth, statistics
```

### build-snapshot-sequencer.sh - Docker Image Builder

Builds the Docker image for the snapshot sequencer:

```bash
# Build the Docker image
./build-snapshot-sequencer.sh

# What it does:
# 1. Builds from Dockerfile.snapshot-sequencer
# 2. Tags as snapshot-sequencer:latest
# 3. Includes ABI files in /app/abi/
# 4. Creates multi-binary image (unified, consensus-test)
```

### start.sh - Consensus Test Launcher

For running the consensus test system:

```bash
# Start consensus test
./start.sh

# Uses: docker-compose.yml
# Launches: sequencer-consensus-test binary
# Purpose: Testing consensus mechanism
```

## Monitoring Tools

### scripts/monitor_batch_docker.sh

Monitors batch preparation status from inside Docker containers:

```bash
# Auto-detect container and monitor
./scripts/monitor_batch_docker.sh

# Monitor specific container
./scripts/monitor_batch_docker.sh powerloom-sequencer-validator-listener-1

# Or use via launch.sh
./launch.sh monitor
```

**Output includes:**
- Active submission windows
- Ready batches by epoch
- Pending queue depth  
- Processing statistics
- Recent batch history

### scripts/check_batch_status.sh

Direct Redis monitoring script (requires Redis access):

```bash
# Run when Redis is accessible (debug mode or local)
./scripts/check_batch_status.sh

# Configure Redis connection
export REDIS_HOST=localhost
export REDIS_PORT=6379
./scripts/check_batch_status.sh
```

### Manual Monitoring Commands

```bash
# Check container status
docker ps

# View logs for specific service
docker logs -f powerloom-sequencer-validator-listener-1

# View all logs in distributed mode
docker-compose -f docker-compose.distributed.yml logs -f

# Check P2P peer count
docker exec powerloom-sequencer-validator-listener-1 \
  curl -s http://localhost:8001/peers | jq '.peer_count'

# Monitor Redis queue depth
docker exec powerloom-sequencer-validator-redis-1 \
  redis-cli LLEN submissionQueue
```

## Deployment Modes

### Unified Mode (Development/Testing)

Single container with all components:

```bash
# Configure .env
cat > .env << EOF
ENABLE_LISTENER=true
ENABLE_DEQUEUER=true
ENABLE_FINALIZER=false
ENABLE_CONSENSUS=false
ENABLE_EVENT_MONITOR=false
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/...
PRIVATE_KEY=<your-key>
EOF

# Launch
./launch.sh unified
```

### Distributed Mode (Production)

Separate containers for scalability:

```bash
# Configure .env for distributed mode
cat > .env << EOF
# Component scaling
DEQUEUER_REPLICAS=3
FINALIZER_REPLICAS=2

# P2P Configuration
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/...
PRIVATE_KEY=<your-key>
PUBLIC_IP=<your-vps-ip>

# RPC Configuration
POWERLOOM_RPC_NODES=http://rpc1.com:8545,http://rpc2.com:8545
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c
EOF

# Launch
./launch.sh distributed
```

**Components in distributed mode:**
- **listener**: Receives P2P submissions (1 instance)
- **dequeuer**: Processes queue (scales horizontally)
- **event-monitor**: Watches for EpochReleased events (1 instance)
- **finalizer**: Creates batches (redundancy via replicas)
- **consensus**: Votes on batches (1 instance)

### Custom Mode (Flexible)

Configure exactly what you need:

```bash
# Example: Only P2P listener
ENABLE_LISTENER=true
ENABLE_DEQUEUER=false
ENABLE_FINALIZER=false
ENABLE_CONSENSUS=false
ENABLE_EVENT_MONITOR=false

./launch.sh sequencer-custom
```

## Multi-VPS Setup

### VPS 1: Bootstrap Node

```bash
# .env configuration
SEQUENCER_ID=bootstrap-node
P2P_PORT=9100
ENABLE_LISTENER=true
ENABLE_DEQUEUER=false
# No BOOTSTRAP_MULTIADDR (it IS the bootstrap)

# Launch
./launch.sh sequencer-custom

# Get multiaddr for other nodes
docker logs powerloom-sequencer-validator-sequencer-custom-1 | grep "P2P host started"
# Share the multiaddr with other validators
```

### VPS 2: Validator Node 1

```bash
# .env configuration  
SEQUENCER_ID=validator-1
BOOTSTRAP_MULTIADDR=/ip4/<VPS1-IP>/tcp/9100/p2p/<BOOTSTRAP-PEER-ID>
PRIVATE_KEY=<unique-key-for-validator-1>
PUBLIC_IP=<this-vps-public-ip>

# Full validator setup
ENABLE_LISTENER=true
ENABLE_DEQUEUER=true
ENABLE_FINALIZER=true
ENABLE_CONSENSUS=true
ENABLE_EVENT_MONITOR=true

# Launch
./launch.sh distributed
```

### VPS 3: Validator Node 2

```bash
# Similar to VPS 2 but with:
SEQUENCER_ID=validator-2
PRIVATE_KEY=<unique-key-for-validator-2>
PUBLIC_IP=<vps3-public-ip>

# Launch
./launch.sh distributed
```

### Verification

```bash
# On each VPS, verify connectivity
docker exec <container-name> curl -s http://localhost:8001/peers

# Should see peer_count > 0 and list of connected peers
```

## Troubleshooting

### Common Issues and Solutions

#### Event Monitor Not Loading ABI

**Problem**: `failed to load contract ABI: no such file or directory`

**Solution**:
```bash
# Pull latest code
git pull

# Rebuild image (launch.sh does this automatically)
./launch.sh stop
./launch.sh distributed

# Verify ABI is included
docker exec <container> ls -la /app/abi/
```

#### Redis Connection Failed

**Problem**: `Failed to connect to Redis: connection refused`

**Solution**:
```bash
# For Docker deployment, ensure REDIS_HOST=redis
echo "REDIS_HOST=redis" >> .env

# For local binary, ensure REDIS_HOST=localhost
echo "REDIS_HOST=localhost" >> .env

# Restart services
./launch.sh stop
./launch.sh unified
```

#### JSON Array Parsing Errors

**Problem**: Environment variable parsing failures

**Solution**:
```bash
# Use comma-separated format (RECOMMENDED)
DATA_MARKET_ADDRESSES=0x123...,0x456...,0x789...
POWERLOOM_RPC_NODES=http://rpc1.com,http://rpc2.com

# Avoid JSON arrays unless necessary
```

#### P2P Connection Issues

**Problem**: `peer_count: 0` or no peers connecting

**Solution**:
```bash
# Ensure bootstrap multiaddr is correct
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/12D3KooWN4ovysY4dp45NhLPQ9ywEhK3Z1GmaCVhQKrKHrtk1R2x

# For NAT traversal, set PUBLIC_IP
PUBLIC_IP=<your-vps-public-ip>

# Open firewall ports
sudo ufw allow 9001/tcp
```

### Viewing Logs

```bash
# All services in distributed mode
docker-compose -f docker-compose.distributed.yml logs -f

# Specific service
docker logs -f powerloom-sequencer-validator-listener-1 --tail 100

# Filter for errors
docker logs powerloom-sequencer-validator-dequeuer-1 2>&1 | grep ERROR

# Save logs to file
docker logs powerloom-sequencer-validator-listener-1 > listener.log 2>&1
```

### Health Checks

```bash
# Check all containers are running
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Verify Redis is healthy
docker exec powerloom-sequencer-validator-redis-1 redis-cli ping

# Check submission queue depth
docker exec powerloom-sequencer-validator-redis-1 redis-cli LLEN submissionQueue

# Monitor memory usage
docker stats --no-stream
```

## Appendix

### Log Examples

#### Successful P2P Connection
```
INFO[2024-08-05T12:34:56Z] P2P host started with ID: 12D3KooWAbcdef...
INFO[2024-08-05T12:34:57Z] Connected to bootstrap peer
INFO[2024-08-05T12:34:58Z] Discovered 3 peers via DHT
INFO[2024-08-05T12:35:00Z] Subscribed to topic: /powerloom/consensus/votes
```

#### Receiving Submissions
```
INFO[2024-08-05T12:36:00Z] Received submission from 12D3KooWXyz...
INFO[2024-08-05T12:36:00Z] Processing submission for epoch 1234
INFO[2024-08-05T12:36:01Z] Submission validated and queued
```

#### Batch Preparation
```
INFO[2024-08-05T12:37:00Z] Preparing batch for epoch 1234
INFO[2024-08-05T12:37:01Z] Batch created with 25 submissions
INFO[2024-08-05T12:37:02Z] Batch finalized: QmBatchCID...
```

### Firewall Configuration

```bash
# Required ports
sudo ufw allow 9001/tcp  # P2P communication
sudo ufw allow 9090/tcp  # Metrics (optional)
sudo ufw allow 22/tcp    # SSH

# Apply rules
sudo ufw enable
```

### Using Screen Sessions (Non-Docker)

```bash
# Build binary
go build -o bin/unified cmd/unified/main.go

# Start in screen
screen -S sequencer
./bin/unified

# Detach: Ctrl+A, D
# Reattach: screen -r sequencer
# List sessions: screen -ls
```

### Performance Tuning Tips

1. **Redis Optimization**:
   ```bash
   # In docker-compose, Redis configured with:
   command: redis-server --appendonly yes --maxmemory 2gb
   ```

2. **Scaling Dequeuers**:
   ```bash
   DEQUEUER_REPLICAS=5  # Increase for higher throughput
   DEQUEUER_WORKERS=10  # Workers per replica
   ```

3. **Network Optimization**:
   ```bash
   CONN_MANAGER_LOW_WATER=100
   CONN_MANAGER_HIGH_WATER=400
   GOSSIPSUB_HEARTBEAT_MS=700
   ```

### Support and Resources

- **GitHub Issues**: https://github.com/powerloom/snapshot-sequencer-validator/issues
- **Documentation**: Check `/docs` directory for additional guides
- **Community**: Join our Discord for support

---

*Last Updated: September 2025*
*Version: 1.0.0*