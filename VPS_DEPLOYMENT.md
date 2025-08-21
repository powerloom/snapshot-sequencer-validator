# VPS Deployment Guide

## Two Deployment Systems Available

### System 1: Sequencer Consensus Test (STABLE)
- **Binary**: `cmd/sequencer-consensus-test/main.go`
- **Docker**: `docker-compose.yml`
- **Launcher**: `start.sh`
- **Purpose**: Tests consensus with real P2P listener, dequeuer, but dummy batch generation
- **Status**: Tested and stable for consensus testing

### System 2: Unified Sequencer (EXPERIMENTAL)  
- **Binary**: `cmd/unified/main.go`
- **Docker**: `docker-compose.unified.yml`
- **Launcher**: `launch.sh`
- **Purpose**: Flexible components with toggles
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
METRICS_PORT=8001

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

#### For Unified Sequencer (launch.sh) - Additional Variables
```bash
# Component toggles (in addition to above)
ENABLE_LISTENER=true      # P2P gossipsub listener
ENABLE_DEQUEUER=true      # Redis queue processor
ENABLE_FINALIZER=false    # Batch finalizer (needs IPFS)
ENABLE_CONSENSUS=false    # Consensus voting
DEQUEUER_WORKERS=5       # Worker pool size
```

### 4. Build Options

#### Current Available Scripts

**Build Scripts:**
- `./build-binary.sh` - Builds Go binaries
- `./build-docker.sh` - Builds Docker images  
- `./build.sh` - Builds both binary and Docker

**Launch Scripts:**
- `./start.sh` - Starts validator using docker-compose.yml (current default)
- `./launch.sh` - Advanced launcher for docker-compose.unified.yml with profiles

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

#### Option B: Unified Sequencer (EXPERIMENTAL)
```bash
# launch.sh uses docker-compose.unified.yml with different profiles:
./launch.sh unified      # All-in-one container
./launch.sh distributed  # Separate components
./launch.sh validators   # 3 validator nodes
./launch.sh status       # Check status
./launch.sh logs         # View logs
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

### 2. Monitor Submissions
```bash
# Watch for received messages
docker logs -f <container> 2>&1 | grep "RECEIVED"

# Check Redis queue (if dequeuer enabled)
docker exec <redis-container> redis-cli LLEN submissionQueue
```

### 3. Test with Local Collector
Your local collector should publish to epoch 0 topic:
- Topic: `/powerloom/snapshot-submissions/0`
- The validator will receive and process these as test submissions

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
METRICS_PORT=8001
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