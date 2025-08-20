# VPS Deployment Guide for Validators

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
git clone https://github.com/your-repo/decentralized-sequencer.git
cd decentralized-sequencer

# Create .env from example
cp .env.validator1.example .env

# Edit .env with your specific configuration
nano .env
```

### 3. Configure .env
```bash
SEQUENCER_ID=validator1  # Change for each node (validator2, validator3, etc)
P2P_PORT=9001
METRICS_PORT=8001
PRIVATE_KEY=<your-128-char-hex-key-from-generator>
BOOTSTRAP_MULTIADDR=/ip4/159.203.190.22/tcp/9100/p2p/12D3KooWN4ovysY4dp45NhLPQ9ywEhK3Z1GmaCVhQKrKHrtk1R2x
LOG_LEVEL=info
DEBUG_MODE=false
```

### 4. Build and Run with Helper Scripts

```bash
# Build the Docker image
./build.sh

# Start the validator
./start.sh

# Check logs
docker-compose logs -f

# Stop when needed
./stop.sh
```

### 5. Alternative: Manual Docker Commands

```bash
# Build manually
docker-compose build

# Run manually
docker-compose up -d

# Check status
docker ps
docker-compose logs -f powerloom-sequencer-validator
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