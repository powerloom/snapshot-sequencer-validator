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

The Powerloom Decentralized Sequencer Validator uses a separated architecture for production deployments:

### System Architecture: Two-Level Aggregation (Updated Sep 29, 2025)

#### Core Components
1. **P2P Gateway (Singleton)**
   - **Binary**: `cmd/p2p-gateway/main.go`
   - **Purpose**: Handles ALL P2P communication
   - **Responsibilities**:
     - Peer discovery
     - Gossipsub message exchange
     - Routes submissions to Redis queue
     - Broadcasts COMPLETE local batches (from aggregator only)
     - Receives other validators' complete batches

2. **Aggregator (Singleton)**
   - **Binary**: `cmd/aggregator/main.go`
   - **Purpose**: TWO-LEVEL aggregation
   - **Level 1 (Internal)**:
     - Combines partial results from multiple finalizer workers
     - Creates complete local batch
     - Stores to IPFS
     - Broadcasts complete local view to network
   - **Level 2 (Network)**:
     - **Aggregation Window**: Waits `AGGREGATION_WINDOW_SECONDS` (default 30s)
     - First remote batch arrival starts timer
     - Collects additional validator batches during window
     - Window expiration triggers final aggregation
     - Combines local + remote batches into network-wide consensus view

3. **Finalizer Workers (Multiple/Auto-scaled)**
   - **Binary**: Part of `cmd/unified/main.go`
   - **Purpose**: Process project batches in parallel
   - **Responsibilities**:
     - Process assigned project batches
     - Store partial results ONLY
     - NO BROADCASTING (removed Sep 29)
     - Workers are auto-scaled based on load

4. **Event Monitor**
   - **Monitors blockchain events**
   - Tracks epoch releases
   - Manages submission windows

#### Deployment Options
- **Docker**: `docker-compose.separated.yml`
- **Launcher**: `dsv.sh` (Decentralized Sequencer Validator control script)
- **Purpose**: Modular, scalable snapshot sequencer with clear component responsibilities
- **Status**: New architecture for improved performance and maintainability

#### New Binaries
- `p2p-gateway`: Centralized P2P communication
- `aggregator`: Consensus and batch aggregation
- `finalizer`: Batch creation
- Existing unified and consensus-test binaries maintained for backward compatibility

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

### 2. IPFS Service Options

#### Option A: Use External IPFS Service
```bash
# Configure to use external IPFS node
IPFS_HOST=127.0.0.1:5001  # Your external IPFS node address
```

#### Option B: Use Built-in IPFS Service (Recommended)
```bash
# Configure for built-in IPFS service
IPFS_HOST=ipfs:5001  # Use local IPFS service from --with-ipfs flag
```

### 5. Launch Sequencer
```bash
# Start production services (separated architecture)
./dsv.sh start

# Start with local IPFS service (recommended for testing)
./dsv.sh start --with-ipfs

# For development/testing only (unified mode)
./dsv.sh dev
```

### 4. Configure Essential Variables
```bash
# Required P2P settings
BOOTSTRAP_MULTIADDR=/ip4/<BOOTSTRAP_NODE_IPV4_ADDR>/tcp/<PORT>/p2p/<PEER_ID>
PRIVATE_KEY=<your-generated-private-key>

# Required for production
POWERLOOM_RPC_NODES=http://your-rpc-endpoint:8545
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c
```

### IPFS Configuration

When using the built-in IPFS service (`--with-ipfs` flag), the following environment variables control cleanup behavior:

```bash
# IPFS Cleanup Configuration (for built-in IPFS service)
# Automatically unpins old CIDs to prevent storage bloat

# Maximum age for pins before cleanup (in days)
# CIDs older than this will be unpinned automatically
IPFS_CLEANUP_MAX_AGE_DAYS=7

# Cleanup interval (in hours)
# How often to run the cleanup process
IPFS_CLEANUP_INTERVAL_HOURS=72  # Every 3 days
```

**Note**: The built-in IPFS service includes automated cleanup functionality that:
- Unpins CIDs older than `IPFS_CLEANUP_MAX_AGE_DAYS` (default: 7 days)
- Runs cleanup every `IPFS_CLEANUP_INTERVAL_HOURS` (default: 72 hours)
- Uses conservative approach to avoid removing important data
- Logs all cleanup activities for monitoring

### 3. Launch Sequencer
```bash
# Start production services (separated architecture)
./dsv.sh start

# For development/testing only (unified mode)
./dsv.sh dev
```

#### Launch Modes
- `start`: Production mode with separated architecture (P2P Gateway, Aggregator, Dequeuer, Finalizer, Event Monitor)
- `dev`: Development mode with unified single container
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
# New Single-Responsibility Component Toggles
ENABLE_P2P_GATEWAY=true   # Central P2P communication handler
ENABLE_AGGREGATOR=true    # Batch consensus and aggregation
ENABLE_FINALIZER=true     # Batch creation from submissions
ENABLE_EVENT_MONITOR=true # Blockchain event tracking

# Separated Architecture Configurations
P2P_GATEWAY_REPLICAS=1    # Typically a singleton
AGGREGATOR_REPLICAS=1     # Typically a singleton
FINALIZER_REPLICAS=3      # Can scale horizontally
EVENT_MONITOR_REPLICAS=1  # Typically a singleton

#### P2P Network Configuration
```bash
# Gossipsub topic configuration
# Configure custom topic names for different deployment scenarios
# All topics are now configurable via environment variables

# Snapshot submission topics
# Format: {prefix}/0 (discovery), {prefix}/all (submissions)
GOSSIPSUB_SNAPSHOT_SUBMISSION_PREFIX=/powerloom/snapshot-submissions

# Finalized batch topics
# Format: {prefix}/0 (discovery), {prefix}/all (batches)
GOSSIPSUB_FINALIZED_BATCH_PREFIX=/powerloom/finalized-batches

# Validator consensus topics
GOSSIPSUB_VALIDATOR_PRESENCE_TOPIC=/powerloom/validator/presence
GOSSIPSUB_CONSENSUS_VOTES_TOPIC=/powerloom/consensus/votes
GOSSIPSUB_CONSENSUS_PROPOSALS_TOPIC=/powerloom/consensus/proposals

# Network discovery
RENDEZVOUS_POINT=powerloom-snapshot-sequencer-network
BOOTSTRAP_MULTIADDR=/ip4/<BOOTSTRAP_NODE_IPV4_ADDR>/tcp/<PORT>/p2p/<PEER_ID>

# P2P networking
P2P_PORT=9001
PUBLIC_IP=<your-vps-ip>  # Optional for NAT traversal
PRIVATE_KEY=<your-generated-private-key>
```

# Batch Aggregation Configuration (moved to Aggregator)
VOTING_THRESHOLD=0.67     # Percentage of validators required for batch aggregation
MIN_VALIDATORS=3          # Minimum validators for valid batch
BATCH_AGGREGATION_TIMEOUT=300  # Timeout for aggregation voting
```

#### Batch Aggregation Configuration
- Now centralized in the Aggregator component
- `VOTING_THRESHOLD`: Percentage of validators needed to approve a batch (default: 0.67 or 67%)
- `MIN_VALIDATORS`: Minimum validators required to start aggregation
- `BATCH_AGGREGATION_TIMEOUT`: Maximum time for aggregation voting before timeout
- Configuration applies across all validators consistently

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
# IMPORTANT: This OVERRIDES any contract-specified duration for testing flexibility
# Production should typically match contract settings, but this allows shorter windows for testing
SUBMISSION_WINDOW_DURATION=60  # seconds
MAX_CONCURRENT_WINDOWS=100

# Level 2 Aggregation Window
# Time to wait for validator finalizations before aggregating network consensus
# First remote batch starts timer, additional batches collected until expiration
AGGREGATION_WINDOW_SECONDS=30  # seconds (default: 30s)

# Event monitoring
EVENT_POLL_INTERVAL=12
EVENT_START_BLOCK=0      # 0 = start from current block (recommended)
EVENT_BLOCK_BATCH_SIZE=1000

# Deduplication
DEDUP_ENABLED=true
DEDUP_LOCAL_CACHE_SIZE=10000
DEDUP_TTL_SECONDS=7200
```

#### Monitor API Configuration
```bash
# Monitor API settings
MONITOR_API_PORT=9091    # Port for monitor API service
ENABLE_MONITOR_API=true   # Enable/disable monitor API service

# Monitor API configuration (auto-generated from other settings)
# Uses PROTOCOL_STATE_CONTRACT and DATA_MARKET_ADDRESSES from environment
# No additional configuration required for basic operation
```

## Launch Scripts Reference

### dsv.sh - Decentralized Sequencer Validator Control Script

The `dsv.sh` script is the primary tool for managing your sequencer deployment.

#### Available Commands

```bash
# Main Commands
./dsv.sh start         # Start separated architecture (production)
./dsv.sh start --with-ipfs     # Start with local IPFS service
./dsv.sh stop          # Stop all services
./dsv.sh restart       # Restart all services
./dsv.sh status        # Show service status
./dsv.sh clean         # Stop and remove all containers/volumes

# Monitoring
./dsv.sh dashboard     # Open monitoring dashboard in browser
./dsv.sh logs          # Show all logs (with optional tail count)

# Component-specific logs
./dsv.sh p2p-logs [N]         # P2P Gateway logs
./dsv.sh aggregator-logs [N]  # Aggregator logs
./dsv.sh finalizer-logs [N]   # Finalizer logs
./dsv.sh dequeuer-logs [N]    # Dequeuer logs
./dsv.sh event-logs [N]       # Event monitor logs
./dsv.sh redis-logs [N]       # Redis logs
./dsv.sh ipfs-logs [N]        # IPFS node logs
./dsv.sh monitor-api-logs [N] # Monitor API logs

# Development
./dsv.sh build         # Build Go binaries (NOT Docker images)
./dsv.sh dev           # Start unified sequencer (single container)

# Help
./dsv.sh help          # Show usage information
```


#### Command Details

**start**: Launches separated architecture with Docker Compose
```bash
./dsv.sh start
# Uses: docker-compose.separated.yml
# Runs: docker compose -f docker-compose.separated.yml up -d --build
# Services: p2p-gateway, aggregator, dequeuer, finalizer, event-monitor, redis

# start --with-ipfs**: Launches with local IPFS service
./dsv.sh start --with-ipfs
# Uses: docker-compose.separated.yml with ipfs profile
# Runs: docker compose -f docker-compose.separated.yml --profile ipfs up -d --build
# Services: p2p-gateway, aggregator, dequeuer, finalizer, event-monitor, redis, ipfs
# Additional: IPFS node with automatic cleanup enabled
```

**dev**: Development mode with unified sequencer
```bash
./dsv.sh dev
# Uses: default docker-compose.yml
# Runs single container with all components based on .env toggles
```

**build**: Builds Go binaries (NOT Docker images)
```bash
./dsv.sh build
# Runs: ./build-binary.sh
# Creates binaries in bin/ directory:
#   - bin/unified
#   - bin/p2p-gateway
#   - bin/aggregator
#   - bin/sequencer-consensus-test
```

**dashboard**: Opens monitoring dashboard in browser
```bash
./dsv.sh dashboard
# Opens Swagger UI at http://localhost:${MONITOR_API_PORT:-9091}/swagger/index.html
# Default port: 9091 (configurable via MONITOR_API_PORT environment variable)
# Provides interactive monitoring with 10 REST endpoints
# Supports protocol/market filtering for multi-market environments
```

### Docker Image Building

Docker images are built automatically when using `./dsv.sh start`:

```bash
# The start command includes --build flag
./dsv.sh start
# Internally runs: docker compose -f docker-compose.separated.yml up -d --build
```

For manual Docker image rebuilding:

```bash
# Force rebuild with no cache
docker compose -f docker-compose.separated.yml build --no-cache

# Or remove images first
docker rmi $(docker images | grep snapshot-sequencer | awk '{print $3}') -f
docker compose -f docker-compose.separated.yml build
```

## Monitoring Tools

### RESTful Monitor API Service (FULLY OPERATIONAL)

The complete `monitor-api` provides a comprehensive, professional monitoring solution with 10 REST endpoints and interactive Swagger UI:

```bash
# Access monitor API (default port 9091, configurable via MONITOR_API_PORT)
http://localhost:9091/swagger/index.html

# Or with custom port
http://localhost:8080/swagger/index.html  # if MONITOR_API_PORT=8080

# Swagger UI provides interactive documentation and testing
```

**Configuration:**
Set `MONITOR_API_PORT` in your `.env` file to customize the port (default: 9091).

**All 10 Monitoring Endpoints:**
1. `/api/v1/health`: Service health check
2. `/api/v1/dashboard/summary`: Real-time dashboard metrics
3. `/api/v1/epochs/timeline`: Epoch progression timeline
4. `/api/v1/batches/finalized`: Recently finalized batches
5. `/api/v1/aggregation/results`: Network aggregation results
6. `/api/v1/timeline/recent`: Recent activity feed
7. `/api/v1/queues/status`: Queue monitoring
8. `/api/v1/pipeline/overview`: Pipeline status summary
9. `/api/v1/stats/daily`: Daily aggregated statistics
10. `/api/v1/stats/hourly`: Hourly performance metrics

### Monitor API Status

✅ **All 10 endpoints fully operational with real data:**
- **Health Check**: Shows `data_fresh: true` with actual pipeline data
- **Dashboard**: Real metrics with participation rates, current epoch status
- **Epoch Timeline**: Actual epoch progression with correct status reading
- **Finalized Batches**: Batch data with validator attribution and IPFS CIDs
- **Aggregation Results**: Network-wide consensus with validator counting
- **Timeline Activity**: Recent submissions and batch completions
- **Queue Status**: Real-time queue depths and processing rates
- **Pipeline Overview**: Complete pipeline status with health indicators
- **Daily/Stats**: Actual aggregated data from pipeline metrics

### Query Parameters

All endpoints support protocol/market filtering for multi-market environments:

```bash
# Filter by specific protocol and market
curl "http://localhost:8080/api/v1/dashboard/summary?protocol=powerloom&market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f"

# Multiple markets (comma-separated)
curl "http://localhost:8080/api/v1/batches/finalized?market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f,0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"

# JSON array format
curl "http://localhost:8080/api/v1/aggregation/results?market=[\"0x21cb57C1f2352ad215a463DD867b838749CD3b8f\",\"0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c\"]"
```

### Shell-Based Monitoring Client

The system includes a pure bash monitoring client for quick terminal-based checks:

```bash
# Basic monitoring (uses monitor-api by default)
./scripts/monitor_api_client.sh

# With custom port
./scripts/monitor_api_client.sh 9090

# With protocol and market filtering
./scripts/monitor_api_client.sh 8080 powerloom 0x21cb57C1f2352ad215a463DD867b838749CD3b8f

# Multiple markets
./scripts/monitor_api_client.sh 8080 powerloom "0x21cb57C1f2352ad215a463DD867b838749CD3b8f,0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"
```

**Features:**
- No external dependencies (pure bash/curl/grep/sed/awk)
- Accepts port, protocol, and market as arguments
- Use with `./scripts/monitor_api_client.sh` for terminal monitoring
- Handles both comma-separated and JSON array market formats
- Provides real-time status updates

### Legacy Monitoring Tools

For quick terminal-based monitoring, use the shell script directly:

```bash
# Terminal monitoring using shell client
./scripts/monitor_api_client.sh
```

The shell monitoring client provides quick insights into the Decentralized Sequencer Validator system:


**Key Monitoring Sections:**
1. **Active Submission Windows**
   - Shows open and closed epochs
   - Displays market and epoch details
   - Time-to-live (TTL) for each window

2. **Submission Queue**
   - Pending submissions count
   - Provides queue depth for debugging

3. **Batch Readiness**
   - Identifies ready batches with vote data
   - Shows protocol, market, and epoch details
   - Highlights project count and vote status

4. **Finalized Batches**
   - Displays recently finalized batches
   - Shows IPFS CID, Merkle root
   - Includes finalization timestamp

5. **Active Workers**
   - Lists worker statuses
   - Monitors worker health and activity

6. **Finalization Queue**
   - Tracks batches pending finalization
   - Shows queue lengths across different protocols

7. **P2P Validator Consensus (Phase 3 - FULLY OPERATIONAL)**
   - Batch Broadcasting: `/powerloom/finalized-batches/all`
   - Active Validators: 3-5 per epoch
   - Independent validator batch finalization
   - Local per-project vote aggregation
   - IPFS-backed, Merkle-rooted batch results

**Recommended Monitoring Workflow:**
1. Use Swagger UI for detailed, interactive monitoring (`./dsv.sh dashboard`)
2. Use `./scripts/monitor_api_client.sh` for quick terminal overview
3. Check container logs with `./dsv.sh logs` for additional details
4. Use component-specific log commands for targeted debugging

### Component Log Shortcuts

New dedicated log commands for each component support optional line count for initial view and continuous follow:

```bash
# View P2P Gateway logs
./dsv.sh p2p-logs

# View last 50 lines of P2P logs and continue following
./dsv.sh p2p-logs 50

# View dequeuer logs
./dsv.sh dequeuer-logs

# View last 100 lines of dequeuer logs and continue following
./dsv.sh dequeuer-logs 100

# View finalizer logs
./dsv.sh finalizer-logs

# View last 75 lines of finalizer logs and continue following
./dsv.sh finalizer-logs 75

# View event monitor logs
./dsv.sh event-logs

# View last 25 lines of event monitor logs and continue following
./dsv.sh event-logs 25

# View IPFS logs
./dsv.sh ipfs-logs

# View last 50 lines of IPFS logs and continue following
./dsv.sh ipfs-logs 50

# View Redis logs
./dsv.sh redis-logs

# View last 50 lines of Redis logs and continue following
./dsv.sh redis-logs 50

# View all logs
./dsv.sh logs

# View last N lines of all logs
./dsv.sh logs 200
```

**Log Command Usage Notes:**
- Without a number, the command follows logs in real-time (default: 100 lines)
- Providing a number shows the last N lines, then continues following
- Useful for quickly checking recent log history before monitoring live output
- Combined log commands help debug issues across component boundaries

**Enhanced Dequeuer Logging & EIP-712 Signature Verification:**
The dequeuer now provides comprehensive logging and signature verification:

- **Submission Metadata**:
  - Epoch ID
  - Project ID
  - Slot ID
  - Data Market
  - Submitter address
  - EIP-712 signature verification status

Example log output with EIP-712 verification:
```
INFO[2025-10-02T10:30:15Z] Verifying EIP-712 signature for submission
INFO[2025-10-02T10:30:15Z] Worker 2 processing: Epoch=172883, Project=uniswap_v3, Slot=1, Market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f
INFO[2025-10-02T10:30:15Z] Signature Verification:
  - Cryptographic Validation: ✅ PASSED
  - Registered Address Match: ✅ PASSED
  - Slot Authorization: ✅ PASSED
INFO[2025-10-02T10:30:15Z] ✅ Queued submission: Epoch=172883, Project=uniswap_v3 from peer 12D3KooWFFRQCs9N
```

### EIP-712 Signature Verification

#### Configuration
Set `ENABLE_SLOT_VALIDATION` in `.env` to control signature authorization:

- `false` (default): Basic cryptographic signature validation
  - Checks signature matches the signing address
  - Does NOT verify address against protocol state

- `true`: Full authorization validation
  - Cryptographic signature verification
  - Checks signature against registered snapshotter address in protocol-state-cacher
  - Validates signer is authorized for the specific slot

#### Requirements
- `protocol-state-cacher` must be running
- Redis must be populated with `SlotInfo.{slotID}` keys
- Submissions must include a valid EIP-712 signature

#### Signature Verification Process
1. **Cryptographic Validation**
   - Verifies signature using secp256k1 curve
   - Ensures message integrity and authentic origin
   - Cryptographically proves message was signed by the claimed address

2. **Address Verification**
   - Cross-references signature signer with registered snapshotter address
   - Prevents impersonation and unauthorized submissions
   - Requires active protocol-state-cacher service

3. **Slot Authorization**
   - Checks if the verified address is authorized for the specific slot
   - Prevents unauthorized submissions across different validator slots
   - Dynamically updated from protocol state contract

#### Recommended Production Setup
- Always set `ENABLE_SLOT_VALIDATION=true`
- Ensure stable connection to protocol-state-cacher
- Implement monitoring for signature verification failures

#### Troubleshooting Signature Verification

**Common Issues:**
- Signature does not match registered address
- Slot authorization check fails
- Protocol-state-cacher unavailable

**Debugging Steps:**
1. Check `protocol-state-cacher` logs
2. Verify Redis `SlotInfo.*` keys are correctly populated
3. Confirm EIP-712 signature is correctly formatted
4. Review submitter's registered address in protocol state

**Example Failure Logs:**
```
WARN[2025-10-02T10:31:00Z] Signature Verification Failed
  - Reason: Signature signer (0xabc...) does not match registered snapshotter address
  - Slot: 1
  - Epoch: 172883
  - Action: Submission Rejected
```

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
## Deployment Modes

### Development/Testing Mode

Single container with configurable components:

```bash
# Configure .env
cat > .env << EOF
ENABLE_LISTENER=true
ENABLE_DEQUEUER=true
ENABLE_FINALIZER=false
ENABLE_EVENT_MONITOR=false
ENABLE_BATCH_AGGREGATION=false
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/...
PRIVATE_KEY=<your-key>
EOF

# Launch development mode
./dsv.sh dev
```

### Separated Mode (Production - RECOMMENDED)

**THIS IS THE DEFAULT AND RECOMMENDED DEPLOYMENT MODE**

The separated architecture solves the critical port conflict issue in the old distributed mode where all containers tried to bind to port 9001. This new architecture uses dedicated binaries with clean single-responsibility design:

```bash
# Configure .env for separated mode
cat > .env << EOF
# SOLVED: Port 9001 conflict from distributed mode
# - P2P Gateway: Dedicated binary owns port 9001 exclusively
# - Aggregator: Separate binary for consensus (no P2P port needed)
# - Finalizer: Uses unified binary with ENABLE_BATCH_AGGREGATION=false
# - Clean separation prevents any port binding conflicts

# Component Scaling
P2P_GATEWAY_REPLICAS=1     # SINGLETON: Centralized P2P gateway
AGGREGATOR_REPLICAS=1      # SINGLETON: Consensus batch aggregation
FINALIZER_REPLICAS=3       # SCALABLE: Batch creation workers
EVENT_MONITOR_REPLICAS=1   # SINGLETON: Blockchain event tracking

# P2P Configuration
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/...
PRIVATE_KEY=<your-key>
PUBLIC_IP=<your-vps-ip>

# RPC Configuration
POWERLOOM_RPC_NODES=http://rpc1.com:8545,http://rpc2.com:8545
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c
EOF

# Launch Production Mode
./dsv.sh start
```

**Resolved Architecture Challenges:**
- **Port Conflict**: P2P Gateway owns port 9001 exclusively
- **Scalability**: Horizontal scaling for finalizer workers
- **Clear Responsibilities**: Single binary per component

**Components in Separated Mode:**
- **p2p-gateway**: Centralized P2P communication (port 9001)
  - Resolves previous port binding issues
  - Single point of message routing
- **aggregator**: Performs consensus and batch aggregation
  - Merkle tree generation
  - IPFS storage of consensus results
- **finalizer**: Creates project-specific batches
  - Horizontally scalable workers
  - No longer handles complex aggregation logic
- **event-monitor**: Tracks blockchain epoch events

### Custom Configuration

To run with specific components only, configure .env then use:

```bash
# For production (separated architecture)
./dsv.sh start

# For development (unified container)
./dsv.sh dev
```

#### Deployment Architecture Benefits
- Clear separation of concerns
- Easier horizontal scaling of batch creation
- Centralized P2P communication
- Simplified network topology
- More predictable performance characteristics
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
./dsv.sh dev

# Get multiaddr for other nodes
docker logs snapshot-sequencer-validator-unified-1 | grep "P2P host started"
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
./dsv.sh start
```

### VPS 3: Validator Node 2

```bash
# Similar to VPS 2 but with:
SEQUENCER_ID=validator-2
PRIVATE_KEY=<unique-key-for-validator-2>
PUBLIC_IP=<vps3-public-ip>

# Launch
./dsv.sh start
```

### Verification

```bash
# On each VPS, verify connectivity
docker exec <container-name> curl -s http://localhost:8001/peers

# Should see peer_count > 0 and list of connected peers
```

## Troubleshooting

### Monitoring API Troubleshooting

#### Monitor API Not Starting

**Problem**: Monitor API fails to start or returns errors

**Solution**:
```bash
# Check if monitor-api container is running
docker ps | grep monitor

# View monitor logs
./dsv.sh monitor-api-logs

# Check environment variables
docker exec <monitor-container> printenv | grep MONITOR_API_PORT

# Verify Redis connection
docker exec <monitor-container> curl -s http://localhost:8080/api/v1/health
```

#### Empty or Zero Data Responses

**Problem**: Monitor API returns empty responses or zero values

**Solution**:
```bash
# Check if pipeline components are running
./dsv.sh status

# Verify Redis data exists
docker exec redis redis-cli SCAN 0 MATCH "powerloom:*" COUNT 10

# Check state-tracker logs
./dsv.sh monitor-api-logs | grep "state-tracker"

# Verify environment variables are consistent
grep -E "PROTOCOL_STATE_CONTRACT|DATA_MARKET_ADDRESSES" .env
```

#### Query Parameter Issues

**Problem**: Filtering by protocol/market returns no data

**Solution**:
```bash
# Test without filters first
curl "http://localhost:8080/api/v1/dashboard/summary"

# Check available markets in Redis
docker exec redis redis-cli SCAN 0 MATCH "powerloom:*" COUNT 5

# Verify market address format
# Should be: 0x21cb57C1f2352ad215a463DD867b838749CD3b8f (with 0x prefix)

# Test single market filter
curl "http://localhost:8080/api/v1/dashboard/summary?market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f"
```

#### CORS or Connection Issues

**Problem**: Browser errors when accessing Swagger UI

**Solution**:
```bash
# Check if port is accessible
curl -I http://localhost:9091/swagger/index.html

# Verify firewall settings
sudo ufw status | grep 8080

# Check container port mapping
docker ps | grep monitor
```

### Common Issues and Solutions

#### Event Monitor Not Loading ABI

**Problem**: `failed to load contract ABI: no such file or directory`

**Solution**:
```bash
# Pull latest code
git pull

# Rebuild image (dsv.sh does this automatically)
./dsv.sh stop
./dsv.sh distributed

# Verify ABI is included
docker exec <container> ls -la /app/abi/
```

#### Window Duration Not Applying

**Problem**: Submission window shows wrong duration (e.g., 1m instead of configured 20s)

**Solution**:
```bash
# Ensure SUBMISSION_WINDOW_DURATION is in .env
echo "SUBMISSION_WINDOW_DURATION=20" >> .env

# Rebuild containers to pick up env changes
./dsv.sh stop
docker compose -f docker-compose.distributed.yml build --no-cache
./dsv.sh distributed

# Verify in event monitor logs
./dsv.sh event-monitor-logs | grep "window opened"
```

**Note**: The SUBMISSION_WINDOW_DURATION env var overrides any contract-specified duration for testing flexibility

#### Redis Connection Failed

**Problem**: `Failed to connect to Redis: connection refused` or `ERR AUTH <password> called without any password configured`

**Solution 1 - Inline Comments in .env File**:

Docker Compose does NOT strip inline comments from .env files! This is a common issue:

```bash
# WRONG - Docker Compose will include "# comment" as the password value!
REDIS_PASSWORD=            # Password (leave empty if no auth)

# CORRECT - Comment on separate line
# Password (leave empty if no auth)
REDIS_PASSWORD=
```

If you see `ERR AUTH <password> called without any password configured`, check your .env file for inline comments. Fix:
```bash
# Edit .env file and remove inline comments
nano .env
# Change any lines like: REDIS_PASSWORD=    # comment
# To just: REDIS_PASSWORD=
# Save and restart
./dsv.sh restart
```

**Solution 2 - Connection Refused**:
```bash
# For Docker deployment, ensure REDIS_HOST=redis
echo "REDIS_HOST=redis" >> .env

# For local binary, ensure REDIS_HOST=localhost
echo "REDIS_HOST=localhost" >> .env

# Restart services
./dsv.sh stop
./dsv.sh start
```

**Solution 2 - AUTH Error with Empty Password** (Fixed in latest version):

If you see `ERR AUTH <password> called without any password configured` when REDIS_PASSWORD is empty:

```bash
# This was a bug in cmd/unified/main.go where empty passwords were still sent to Redis
# Fixed in commit: Modified Redis connection to only set password if not empty

# To apply the fix:
1. Pull latest code: git pull
2. Rebuild Docker images with no cache:
   docker compose -f docker-compose.separated.yml build --no-cache
3. Restart: ./dsv.sh restart

# The fix ensures empty REDIS_PASSWORD doesn't trigger AUTH
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

### Submission Processing Issues

**Problem**: Incorrect data model parsing, submissions not being processed

**Solution**:
```bash
# Check submission format strategy
cat .env | grep SUBMISSION_FORMAT_STRATEGY

# If 'auto' fails, manually set format
SUBMISSION_FORMAT_STRATEGY=single  # or 'batch'

# Debug submission processing logs
./dsv.sh dqr-logs | grep -E 'Processing|P2PSnapshotSubmission|epochId'

# Check for field name conversions (snake_case → camelCase)
grep -R 'epoch_id' .  # Should return no results if converted
```

#### Common Conversion Issues
- `epoch_id` → `epochId`
- `project_id` → `projectId`
- `submitter_address` → `submitterAddress`

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

# Debug new submission processing
docker logs powerloom-sequencer-validator-dequeuer-1 2>&1 | grep -E 'epochId|projectId|P2PSnapshotSubmission'
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

4. **Finalization Worker Optimization**:
   ```bash
   # Control parallel finalization workers
   FINALIZER_WORKERS=5      # Number of concurrent finalization workers
   FINALIZATION_BATCH_SIZE=20  # Projects processed per batch
   
   # Recommended tuning:
   # - Increase FINALIZER_WORKERS for high-throughput networks
   # - Adjust FINALIZATION_BATCH_SIZE based on processing power
   # - Monitor worker health with ./dsv.sh pipeline
   ```

### Support and Resources

- **GitHub Issues**: https://github.com/powerloom/snapshot-sequencer-validator/issues
- **Documentation**: Check `/docs` directory for additional guides
- **Community**: Join our Discord for support

## Docker Image Rebuilding

### When to Rebuild

You need to rebuild Docker images when:
- Code changes are made (e.g., bug fixes)
- Dependencies are updated
- Configuration changes require new binaries

### Rebuild Process

```bash
# Method 1: Using dsv.sh (includes --build flag)
./dsv.sh stop
./dsv.sh start  # Automatically runs with --build

# Method 2: Force complete rebuild (REQUIRED when --no-cache doesn't work)
./dsv.sh stop

# Remove ALL related images to force rebuild
docker rmi $(docker images | grep -E '(snapshot-sequencer|sequencer-validator)' | awk '{print $3}') -f 2>/dev/null || true

# CRITICAL: Clear builder cache completely
docker builder prune -af

# Also remove any dangling images
docker image prune -f

# Now rebuild from scratch
docker compose -f docker-compose.separated.yml build --no-cache

# Start the services
./dsv.sh start

# Method 3: Rebuild specific service
docker compose -f docker-compose.separated.yml build --no-cache p2p-gateway
./dsv.sh restart
```

### Verifying Rebuild

```bash
# Check image creation times
docker images | grep snapshot-sequencer

# Verify code changes are applied (example for Redis fix)
docker exec <container> grep -A5 "RedisPassword" /app/main.go
```

## Recent Updates (October 8, 2025)

### New Features
- ✅ **Complete Monitoring API Implementation** (FULLY OPERATIONAL)
  - All 10 REST endpoints working correctly with real data
  - Interactive Swagger UI documentation and testing
  - Protocol/market query parameter support for multi-market environments
  - Proper Redis namespacing with `{protocol}:{market}:*` keys
  - Production-safe SCAN operations instead of KEYS
  - Validator attribution and IPFS CID integration
  - Shell-based monitoring client with no external dependencies
  - Added `MONITOR_API_PORT` and `ENABLE_MONITOR_API` configuration

### Monitoring API Status
✅ **All endpoints operational:**
- `/api/v1/health` - Service health with `data_fresh: true`
- `/api/v1/dashboard/summary` - Real metrics with participation rates
- `/api/v1/epochs/timeline` - Correct epoch progression status
- `/api/v1/batches/finalized` - Batch data with validator attribution
- `/api/v1/aggregation/results` - Network consensus with validator counting
- `/api/v1/timeline/recent` - Recent activity feed
- `/api/v1/queues/status` - Real-time queue monitoring
- `/api/v1/pipeline/overview` - Complete pipeline status
- `/api/v1/stats/daily` - Actual aggregated statistics
- `/api/v1/stats/hourly` - Hourly performance metrics

### Technical Fixes
- Fixed state-tracker configuration to use correct environment variables
- Enhanced aggregation logic with proper validator counting
- Fixed docker-compose to use consistent environment variables across all services
- Corrected Redis key namespacing across all endpoints
- Implemented proper IPFS CID extraction for Level 2 metrics

### Previous Updates (October 2, 2025)

### New Features
- ✅ EIP-712 Signature Verification Implementation
  - Comprehensive cryptographic signature validation
  - Address verification against protocol state cache
  - Configurable slot authorization check
  - Enhanced security for submission processing
  - Added `ENABLE_SLOT_VALIDATION` configuration option
- ✅ Updated submitter identification to use EVM addresses instead of slot IDs
- ✅ Detailed logging for signature verification process

### Security Enhancements
- Prevent unauthorized submissions through multi-layer signature validation
- Dynamic slot authorization from protocol state contract
- Cryptographically secure submission verification

### Configuration Updates
- Added `ENABLE_SLOT_VALIDATION` environment variable
- Enhanced `.env.example` with detailed signature verification configuration

## Recent Updates (September 22, 2025)

### Bug Fixes
- **Redis Empty Password Handling**: Fixed issue where empty REDIS_PASSWORD caused AUTH errors in separated mode
  - File: `cmd/unified/main.go` lines 107-114
  - Now only sets password in Redis options if not empty
  - Matches behavior of P2P Gateway and Aggregator components

## Recent Updates (September 9, 2025)

### New Features
- ✅ Parallel Finalization Workers: Distributed batch processing
- ✅ New configurations for worker parallelism: `FINALIZER_WORKERS`, `FINALIZATION_BATCH_SIZE`
- ✅ Enhanced batch processing with concurrent worker support
- ✅ Improved worker monitoring and status tracking
- ✅ Individual component log shortcuts (`listener-logs`, `dqr-logs`, `finalizer-logs`, `event-monitor-logs`, `redis-logs`)
- ✅ Enhanced dequeuer logging with detailed submission information (Epoch, Project, Slot, Market, Submitter)
- ✅ Fixed monitor script to prioritize containers with Redis access
- ✅ SUBMISSION_WINDOW_DURATION now properly overrides contract values for testing flexibility
- ✅ Added `pipeline` command for comprehensive pipeline monitoring in `pkgs/workers/monitoring.go`

### Configuration Changes
- Added `FINALIZER_WORKERS` to control parallel finalization workers
- Added `FINALIZATION_BATCH_SIZE` to configure projects processed per batch
- `SUBMISSION_WINDOW_DURATION` must be passed to event-monitor and finalizer containers in docker-compose.distributed.yml
- Added `CONTRACT_ABI_PATH` for dynamic event signature loading

### Bug Fixes
- Fixed window duration not being passed to event-monitor container
- Fixed monitor script selecting wrong container (was using finalizer instead of event-monitor)
- Collection logic improved to track multiple CIDs per project with vote counts
- Implemented batch consensus selection logic

---

*Last Updated: October 8, 2025*
*Version: 1.3.0*