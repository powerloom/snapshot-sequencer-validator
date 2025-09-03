# VPS Deployment Guide for Powerloom Decentralized Sequencer Validator 

## Two Deployment Systems Available

### System 1: Only Consensus Test (STABLE)
- **Binary**: `cmd/sequencer-consensus-test/main.go`
- **Docker**: `docker-compose.yml`
- **Launcher**: `start.sh`
- **Purpose**: Tests consensus with real P2P listener, dequeuer, but dummy batch generation
- **Status**: Tested and stable for consensus testing

### System 2: Full Decentralized Sequencer (EXPERIMENTAL)  
This combines the components in a similar fashion to the existing Powerloom Sequencer that handles all submissions on Powerloom Mainnet Datamarkets.

- **Binary**: `cmd/unified/main.go`
- **Docker**: `docker-compose.snapshot-sequencer.yml`
- **Launcher**: `launch.sh`
- **Purpose**: Flexible snapshot sequencer with component toggles
- **Status**: New, experimental

## Quick Start (Per VPS)

### 1. Generate Unique Key (run locally or on VPS)
```bash
# The key generator is included in this directory
cd key_generator
go run generate_key.go
# Save the output hex private key and peer ID
cd ..
```

### 2. On Each VPS

```bash
# Clone repository
git clone https://github.com/powerloom/snapshot-sequencer-validator.git
cd snapshot-sequencer-validator

# Create .env from example
cp .env.validator1.example .env

# Edit .env with your specific configuration
nano .env
```

### 3. Configure .env

#### For Sequencer Consensus Test (start.sh)
```bash
SEQUENCER_ID=validator1  # Change for each node
P2P_PORT=9001
# METRICS_PORT=9090  # Reserved for future monitoring implementation

# Redis configuration (required for queueing)
REDIS_HOST=localhost      # Use 'redis' for Docker
REDIS_PORT=6379
REDIS_DB=0
REDIS_PASSWORD=           # Leave empty if no auth

# P2P configuration
PRIVATE_KEY=<your-128-char-hex-key-from-generator>
BOOTSTRAP_MULTIADDR=/ip4/YOUR_BOOTSTRAP_IP/tcp/9100/p2p/YOUR_BOOTSTRAP_PEER_ID

# Logging
LOG_LEVEL=info
DEBUG_MODE=false
```

#### For Snapshot Sequencer (launch.sh) - Additional Variables
```bash
# Component toggles (in addition to above)
# These control what runs INSIDE each container
ENABLE_LISTENER=true      # P2P gossipsub listener (receives submissions)
ENABLE_DEQUEUER=true      # Redis queue processor (processes submissions)
ENABLE_FINALIZER=false    # Batch finalizer (needs IPFS for storage)
ENABLE_CONSENSUS=false    # Consensus voting (votes on batches)
ENABLE_EVENT_MONITOR=false # Event monitor for EpochReleased events
DEQUEUER_WORKERS=5       # Worker pool size for dequeuer

# RPC Configuration (Required for event monitoring and contract interactions)
# All RPC interactions in this component are with the Powerloom protocol chain
POWERLOOM_RPC_NODES=["http://your-rpc-endpoint:8545","http://backup-rpc:8545"]
POWERLOOM_ARCHIVE_RPC_NODES=[]  # Optional archive nodes for historical queries

# Protocol contracts (Required when event monitor is enabled)
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=["0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"]

# Batch preparation configuration
SUBMISSION_WINDOW_DURATION=60  # Seconds after epoch release to accept submissions
MAX_CONCURRENT_WINDOWS=100     # Maximum simultaneous submission windows

# Examples of common configurations:
# Just listen to P2P (no processing):
#   ENABLE_LISTENER=true, all others false
# Process without consensus:
#   ENABLE_LISTENER=true, ENABLE_DEQUEUER=true, others false
# Full sequencer:
#   All set to true
```

### 4. Build Options

#### Current Available Scripts

**Build Scripts:**
- `./build-binary.sh` - Builds Go binaries
- `./build-consensus-test.sh` - Builds Docker image for consensus testing
- `./build-snapshot-sequencer.sh` - Builds Docker image for snapshot sequencer
- `./build-docker.sh` - DEPRECATED (use specific build scripts above)
- `./build.sh` - Builds both binary and Docker

**Launch Scripts:**
- `./start.sh` - Starts validator using docker-compose.yml (current default)
- `./launch.sh` - Advanced launcher for docker-compose.snapshot-sequencer.yml with profiles

### 5. Running the Systems

#### Option A: Sequencer Consensus Test (FOR CONSENSUS TESTING)
```bash
# Build and start
./build-docker.sh
./start.sh

# Check logs
docker-compose logs -f

# Stop
docker-compose down
```

#### Option B: Snapshot Sequencer (EXPERIMENTAL)

**How launch.sh profiles work:**

```bash
# Each profile launches different container configurations:

./launch.sh sequencer     # ONE container with ALL components HARDCODED enabled
                         # ENABLE_LISTENER=true, ENABLE_DEQUEUER=true,
                         # ENABLE_FINALIZER=true, ENABLE_CONSENSUS=true

./launch.sh sequencer-custom # ONE container that READS YOUR .env settings
                         # Uses whatever ENABLE_* values you set in .env

./launch.sh distributed   # SEPARATE containers, each doing one thing:
                         # - listener (ENABLE_LISTENER=true only)
                         # - dequeuer x3 replicas (ENABLE_DEQUEUER=true only)
                         # - event-monitor (ENABLE_EVENT_MONITOR=true only)
                         # - finalizer x2 replicas (ENABLE_FINALIZER=true only)
                         # - consensus (optional, ENABLE_CONSENSUS=true only)

./launch.sh distributed --debug  # Distributed mode with Redis exposed
./launch.sh distributed-debug   # Alternative syntax for above

./launch.sh status        # Check status of running containers
./launch.sh logs          # View logs from containers
./launch.sh monitor       # Monitor batch preparation status
./launch.sh debug         # Launch single sequencer with Redis exposed
```

**Distributed Mode Configuration:**
```bash
# Required environment variables for distributed mode
cat > .env << EOF
# P2P Configuration
BOOTSTRAP_MULTIADDR=/ip4/YOUR_IP/tcp/9100/p2p/YOUR_PEER_ID
PRIVATE_KEY=your-hex-private-key
P2P_PORT=9001

# RPC Configuration (Required for event monitor)
POWERLOOM_RPC_NODES=["http://your-rpc:8545"]
POWERLOOM_ARCHIVE_RPC_NODES=[]

# Scaling Configuration
DEQUEUER_REPLICAS=3  # Number of dequeuer containers
FINALIZER_REPLICAS=2  # Number of finalizer containers

# Protocol Contracts
PROTOCOL_STATE_CONTRACT=0xE88E5f64AEB483d7057645326AdDFA24A3B312DF
DATA_MARKET_ADDRESSES=["0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"]
EOF

# Launch distributed mode
./launch.sh distributed

# Launch with debugging (Redis exposed)
./launch.sh distributed --debug
```

**Custom Single Container Configuration:**
```bash
# Create .env with your component toggles
echo "ENABLE_LISTENER=true" >> .env
echo "ENABLE_DEQUEUER=true" >> .env
echo "ENABLE_FINALIZER=false" >> .env
echo "ENABLE_CONSENSUS=false" >> .env
echo "ENABLE_EVENT_MONITOR=false" >> .env
echo "REDIS_HOST=redis" >> .env
echo "BOOTSTRAP_MULTIADDR=/ip4/YOUR_IP/tcp/9100/p2p/YOUR_PEER_ID" >> .env

# Launch with your custom settings
./launch.sh sequencer-custom
```

## What You'll See in Logs

### Successful P2P Connection
```
🎧 Started listening on DISCOVERY/TEST topic: /powerloom/snapshot-submissions/0
📡 Subscribed to topic: /powerloom/snapshot-submissions/all
🔵 Listener component active and monitoring P2P network
Connected to bootstrap node: <YOUR_BOOTSTRAP_PEER_ID>
```

### When Receiving Submissions (from epoch 0 testing)
```
📨 RECEIVED TEST/DISCOVERY on /powerloom/snapshot-submissions/0 from peer QmXXX (size: 1024 bytes)
📋 Submission Details: Epoch=0, Topic=/powerloom/snapshot-submissions/0
   └─ SlotID=1, ProjectID=USDC_ETH, CID=QmYYY...
✅ Successfully queued submission to Redis
```

### Periodic Health Check (every 30s)

```

====== P2P LISTENER STATUS ======
Host ID: QmYourPeerID...
Connected Peers: 5
Topic /powerloom/snapshot-submissions/0: 3 peers
Topic /powerloom/snapshot-submissions/all: 4 peers

=================================
```

## Testing Your Deployment

### 1. Verify P2P Connectivity
```bash
# Check if connected to bootstrap
docker logs <container> 2>&1 | grep "Connected to bootstrap"

# Check peer count
docker logs <container> 2>&1 | grep "Connected Peers"
```

### 2. Monitor Submissions and Batch Preparation
```bash
# Watch for received messages
docker logs -f <container> 2>&1 | grep "RECEIVED"

# Monitor batch preparation status (comprehensive view)
./launch.sh monitor

# Check Redis queue directly (requires debug mode)
./launch.sh debug  # Exposes Redis port
./scripts/check_batch_status.sh  # Run monitoring script
```

### 3. Test with Local Collector
Your local collector should publish to epoch 0 topic:
- Topic: `/powerloom/snapshot-submissions/0`
- The validator will receive and process these as test submissions

### 4. Monitoring Batch Preparation

#### Available Monitoring Tools

##### Option A: Using launch.sh monitor (Recommended)
```bash
./launch.sh monitor

# Automatically finds running sequencer container
# Executes monitoring inside container where Redis is accessible
# Shows: window status, ready batches, pending queue, statistics
```

##### Option B: Direct Docker Script
```bash
# Monitor using first sequencer container found
./scripts/monitor_batch_docker.sh

# Monitor specific container (useful for distributed mode)
./scripts/monitor_batch_docker.sh container-name

# Examples:
./scripts/monitor_batch_docker.sh decentralized-sequencer-sequencer-custom-1
./scripts/monitor_batch_docker.sh sequencer-listener
```

##### Option C: Debug Mode with External Access
```bash
# Launch with Redis exposed
./launch.sh debug                    # For single sequencer
./launch.sh distributed-debug         # For distributed mode
./launch.sh distributed --debug       # Alternative syntax

# Then use external monitoring script
./scripts/check_batch_status.sh      # Requires Redis on localhost:6379

# Or use Redis CLI directly
redis-cli -h localhost -p 6379
```

#### Monitoring Output Explanation
```
📊 Current Window: window-123         # Active submission window ID
📦 Ready Batches:                     # Batches prepared, awaiting finalization
  - batch:ready:window-122            # Previous window's batch ready
⏳ Pending Submissions: 42            # Submissions in queue
📈 Window Statistics:                 # Historical data
  Window 121: 100 submissions         # Completed window stats
  Window 122: 85 submissions          
⏰ Last Batch Prepared: 2025-09-03    # Timestamp of last batch preparation
```

#### When to Use Each Method

| Method | Use Case | Redis Access | Container Required |
|--------|----------|--------------|--------------------|
| `launch.sh monitor` | Quick status check | Internal | Yes |
| `monitor_batch_docker.sh` | Specific container check | Internal | Yes |
| `launch.sh debug` + scripts | Development/debugging | External | Yes |
| Direct Redis CLI | Advanced debugging | External | Debug mode only |

#### Option C: Both Binary and Docker
```bash
./build.sh  # Builds both
```

### 5. Run the Validator

```bash
# Start the validator (Docker)
./start.sh

# Check logs
docker-compose logs -f

# Stop when needed
./stop.sh
```

### 6. Using Screen Sessions (Non-Docker)

```bash
# Build Go binary
go build -o sequencer cmd/sequencer/main.go

# Start in screen
screen -S sequencer
./sequencer
# Detach: Ctrl+A, D
# Reattach: screen -r sequencer
```

## Multi-VPS Setup

### VPS 1 (Bootstrap Node)
```bash
SEQUENCER_ID=bootstrap
P2P_PORT=9100
METRICS_PORT=8100
PRIVATE_KEY=<bootstrap-key>
# No BOOTSTRAP_MULTIADDR needed (it IS the bootstrap)
```

### VPS 2 (Validator 1)
```bash
SEQUENCER_ID=validator1
P2P_PORT=9001
# METRICS_PORT=9090  # Reserved for future monitoring
PRIVATE_KEY=<validator1-key>
BOOTSTRAP_MULTIADDR=/ip4/<VPS1-IP>/tcp/9100/p2p/<bootstrap-peer-id>
```

### VPS 3 (Validator 2)
```bash
SEQUENCER_ID=validator2
P2P_PORT=9002
METRICS_PORT=8002
PRIVATE_KEY=<validator2-key>
BOOTSTRAP_MULTIADDR=/ip4/<VPS1-IP>/tcp/9100/p2p/<bootstrap-peer-id>
```

## Verification

```bash
# Check container status
docker ps

# View logs
docker logs sequencer

# Check P2P connectivity
curl http://localhost:8001/metrics | grep peers

# Test message propagation
curl -X POST http://localhost:8001/submit \
  -H "Content-Type: application/json" \
  -d '{"test": "message"}'
```

## Firewall Rules

Open these ports on each VPS:
```bash
# P2P communication
ufw allow 9001/tcp  # Or your configured P2P_PORT

# Metrics/API
ufw allow 8001/tcp  # Or your configured METRICS_PORT
```

## Monitoring

Access metrics at:
- `http://<VPS-IP>:8001/metrics` - Prometheus metrics
- `http://<VPS-IP>:8001/health` - Health check
- `http://<VPS-IP>:8001/peers` - Connected peers