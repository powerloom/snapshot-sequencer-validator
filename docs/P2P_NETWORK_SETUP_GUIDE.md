# P2P Network Setup Guide

This guide explains how to deploy and test the Powerloom P2P network components across multiple VPS instances.

## Components Overview

1. **Bootstrap Node** - The rendezvous point for peer discovery
2. **Unified Snapshot Sequencer** - Production sequencer with P2P listener and Redis dequeuer
3. **P2P Gossipsub Topic Debugger** - Testing utility with multiple modes:
   - **PUBLISHER** - Sends test messages every 30 seconds
   - **LISTENER** - Monitors topics for messages (fully functional)
   - **DISCOVERY** - Watches the discovery topic (epoch 0) (fully functional)

## Setup Instructions

### 1. Bootstrap Node (VPS #1)

Deploy your bootstrap node with the following configuration:
```
/ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/<PEER_ID>
```

To run your own bootstrap node:

```bash
# Clone the repository
git clone git@github.com:powerloom/submissions-bootstrap-node.git
cd submissions-bootstrap-node
```

Start its own screen session

```bash
screen -S bootstrap-node
```

#### Environment variables

Save the following `.env` file:

```bash
PRIVATE_KEY=<your-private-key-hex>
LOG_LEVEL=info
LIBP2P_LOGGING=info
PUBLIC_IP=<BOOTSTRAP_IP>
# Logging
LOG_FILE=/app/logs/bootstrap-node.log
BOOTSTRAP_PORT=9100
```

#### Docker build and run

Use `sudo` if needed

```bash
docker compose build --no-cache
docker compose up
```

### 2. P2P Debugger - Publisher Mode (VPS #2)

Publishes test messages to the network.

```bash
# Clone the repository
git clone git@github.com:powerloom/libp2p-gossipsub-topic-debugger.git
cd libp2p-gossipsub-topic-debugger
```


Start its own screen session

```bash
screen -S p2p-debugger-publisher
```

#### Environment variables

Save the following `.env` file:

```bash
BOOTSTRAP_ADDR=/ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/<BOOTSTRAP_PEER_ID>
LISTEN_PORT=4001
PUBLIC_IP=<YOUR_VPS_PUBLIC_IP>
LOG_LEVEL=debug
# Rendezvous point for peer discovery
RENDEZVOUS_POINT=powerloom-snapshot-sequencer-network
# Operation mode: PUBLISHER, LISTENER, or DISCOVERY
# - PUBLISHER: Publishes test messages at regular intervals
# - LISTENER: Listens for messages and shows peers
# - DISCOVERY: Shows peers on discovery topic (epoch 0)
MODE=PUBLISHER
# Listener settings
LIST_PEERS=true
```

#### Docker build and run

Use `sudo` if needed

```bash
sudo ./build-docker.sh 
sudo docker compose up
sudo docker compose down --volumes 
```


### 3. P2P Debugger - Discovery Mode (VPS #3)

Monitors the discovery topic for joining peers. Clone the same repo as above.
#### Environment variables

Save the following `.env` file:

```bash
BOOTSTRAP_ADDR=/ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/<BOOTSTRAP_PEER_ID>
LISTEN_PORT=8001
PUBLIC_IP=<YOUR_VPS_PUBLIC_IP>
LOG_LEVEL=debug
# Rendezvous point for peer discovery
RENDEZVOUS_POINT=powerloom-snapshot-sequencer-network
# Operation mode: PUBLISHER, LISTENER, or DISCOVERY
# - PUBLISHER: Publishes test messages at regular intervals
# - LISTENER: Listens for messages and shows peers
# - DISCOVERY: Shows peers on discovery topic (epoch 0)
MODE=DISCOVERY
# Listener settings
LIST_PEERS=true
```

#### Docker build and run

Use `sudo` if needed.

```bash
docker compose build --no-cache
./start.sh
./stop.sh
```

Expected behavior:
- Connects to bootstrap node
- Subscribes to `/powerloom/snapshot-submissions/0`
- Displays presence messages from publishers
- Shows peer counts every 5 seconds
- Lists connected peers

### 4. Unified Snapshot Sequencer (Production)

The production-ready unified sequencer combines P2P listener with Redis dequeuer.

```bash
cd snapshotter-lite-v2/snapshotter-lite-local-collector
```

#### Environment Setup

Create `.env` file:

```bash
# P2P Configuration
PRIVATE_KEY=<your-64-byte-hex-private-key>
BOOTSTRAP_NODE=/ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/<BOOTSTRAP_PEER_ID>
RENDEZVOUS_POINT=powerloom-snapshot-sequencer-network
PUBLIC_IP=<YOUR_VPS_PUBLIC_IP>
LISTEN_PORT=8001

# Redis Configuration
REDIS_HOST=redis
REDIS_PORT=6379

# Sequencer Settings
SEQUENCER_ID=sequencer-001
NUM_DEQUEUE_WORKERS=5
LOG_LEVEL=info
```

#### Build and Run

```bash
# Build the snapshot sequencer Docker image
./build-snapshot-sequencer.sh

# Launch with custom settings
./launch.sh sequencer-custom

# Check logs
docker logs powerloom-sequencer-validator-sequencer-001

# Stop services
./launch.sh stop
```

#### Monitoring

```bash
# Check P2P connections
curl http://localhost:8001/debug/peers

# Check mesh status
curl http://localhost:8001/metrics | grep mesh

# Monitor Redis queue
docker exec -it redis redis-cli
> LLEN submissions_queue
> MONITOR  # Watch Redis activity
```

### 5. P2P Debugger - Listener Mode (Testing)

Monitors actual submission messages. Clone the same repo as above.
#### Environment variables

Save the following `.env` file:

```bash
BOOTSTRAP_ADDR=/ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/<BOOTSTRAP_PEER_ID>
LISTEN_PORT=8001
PUBLIC_IP=<YOUR_VPS_PUBLIC_IP>
LOG_LEVEL=debug
# Rendezvous point for peer discovery
RENDEZVOUS_POINT=powerloom-snapshot-sequencer-network
# Operation mode: PUBLISHER, LISTENER, or DISCOVERY
# - PUBLISHER: Publishes test messages at regular intervals
# - LISTENER: Listens for messages and shows peers
# - DISCOVERY: Shows peers on discovery topic (epoch 0)
MODE=LISTENER
# Listener settings
LIST_PEERS=true
```

#### Docker build and run

Use `sudo` if needed.

```bash
docker compose build --no-cache
./start.sh
./stop.sh
```

Expected behavior:
- Subscribes to both `/powerloom/snapshot-submissions/all` and `/powerloom/snapshot-submissions/0`
- Displays received messages from publishers
- Shows peer counts for both topics


### Key Log Messages to Watch

**Successful peer discovery:**
```
Successfully connected to bootstrap peer: /ip4/<BOOTSTRAP_IP>/tcp/<PORT>/...
Found peer through discovery: 12D3KooW...
Connected to discovered peer: 12D3KooW...
```

**Topic subscription:**
```
Peers in topic /powerloom/snapshot-submissions/all: [12D3KooW...] (count: 1)
```

**Message flow:**
```
Published message #1 to topic /powerloom/snapshot-submissions/all
Received message from 12D3KooW...: {...}
```

## Troubleshooting

### Peers not discovering each other
1. Check firewall rules - ensure ports are open
2. Verify PUBLIC_IP is correctly set
3. Confirm bootstrap node is accessible
4. Check RENDEZVOUS_POINT matches across all nodes

### Messages not propagating
1. Ensure at least 2 peers are in the topic mesh
2. Check peer counts in logs
3. Verify topic names match exactly
4. Wait 10-30 seconds for mesh formation

### Connection issues
1. Test bootstrap connectivity: `telnet <BOOTSTRAP_IP> <PORT>`
2. Check network connectivity between VPS instances
3. Ensure no NAT/firewall blocking P2P traffic

## Environment Variables Reference

| Variable | Description | Example |
|----------|-------------|---------|
| BOOTSTRAP_ADDR | Bootstrap node multiaddr | /ip4/<BOOTSTRAP_IP>/tcp/<PORT>/p2p/... |
| LISTEN_PORT | Local P2P port | 4001 |
| PUBLIC_IP | Public IP of VPS | <YOUR_VPS_IP> |
| LOG_LEVEL | Logging verbosity | debug |
| RENDEZVOUS_POINT | Discovery namespace | powerloom-snapshot-sequencer-network |
| MODE | Operation mode | PUBLISHER, LISTENER, DISCOVERY |
| LIST_PEERS | Show peer counts | true |
| PUBLISH_INTERVAL | Seconds between publishes (PUBLISHER mode) | 30 |

## Network Architecture

```
┌─────────────────┐
│  Bootstrap Node │ (<BOOTSTRAP_IP>:<PORT>)
│   (VPS #1)      │
└────────┬────────┘
         │
    ┌────┴────┬──────────┬──────────┬──────────┐
    │         │          │          │          │
┌───▼───┐ ┌──▼───┐ ┌────▼───┐ ┌────▼───┐ ┌────▼────┐
│Publish│ │Listen│ │Discover│ │Unified │ │  Other  │
│ Mode  │ │ Mode │ │  Mode  │ │Sequencr│ │Sequencrs│
│(VPS#2)│ │(Test)│ │ (VPS#3) │ │(Prod)  │ │ (Prod)  │
└───────┘ └──────┘ └────────┘ └────────┘ └─────────┘
                                    │
                               ┌────▼────┐
                               │  Redis  │
                               │  Queue  │
                               └─────────┘
```

## Topics Structure

- `/powerloom/snapshot-submissions/0` - Discovery/joining room
- `/powerloom/snapshot-submissions/all` - Actual message submissions

## Next Steps

After confirming the test network works:
1. Deploy additional unified sequencers for redundancy
2. Monitor real snapshot submissions through Redis
3. Implement consensus mechanism (Phase 3)
4. Add pluggable DA layer support (Phase 4 - optional)