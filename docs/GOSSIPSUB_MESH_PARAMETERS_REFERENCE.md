# Gossipsub Mesh Parameters Reference Guide

## Library Versions (Critical for Compatibility)
- **go-libp2p**: v0.43.0
- **go-libp2p-pubsub**: v0.14.2
- **go-libp2p-kad-dht**: v0.34.0

## Overview
This document provides comprehensive references and documentation for the **standard gossipsub mesh parameters** that should be uniformly applied across ALL peers participating in the snapshot submissions network. These parameters ensure mesh stability and prevent unnecessary pruning.

**Important**: Some parameters from older libp2p versions are no longer supported:
- ❌ `OpportunisticGraftSamples` - Removed in v0.14.x
- ❌ `ConnectorBackoff` - Removed in v0.14.x

## Core Mesh Parameters Explained

### Mesh Degree Parameters (D-parameters)
These control the target number of peers in the mesh for each topic.

| Parameter | Value | Default | Purpose | Reference |
|-----------|-------|---------|---------|-----------|
| `D` | 10 | 6 | Target mesh degree - ideal number of peers | [libp2p specs](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md#parameters) |
| `Dlo` | 8 | 5 | Lower bound - triggers GRAFT if below | [gossipsub-v1.1](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md#mesh-maintenance) |
| `Dhi` | 16 | 12 | Upper bound - triggers PRUNE if above | [mesh construction](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.0.md#mesh-construction) |
| `Dlazy` | 10 | 6 | Lazy push threshold for gossip | [gossip emission](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.0.md#gossip-emission) |
| `Dout` | 4 | 2 | Min outbound connections per topic | [outbound connections](https://github.com/libp2p/go-libp2p-pubsub/blob/master/gossipsub.go#L44) |
| `Dscore` | 6 | 4 | Peers retained by score during pruning | [peer scoring](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md#peer-scoring) |

### Why These Values?
- **Higher D values**: Maintain more redundant connections to prevent isolation
- **Tighter Dlo/Dhi range**: More aggressive mesh maintenance
- **Higher Dlazy**: More peers receive metadata even when not in mesh

## Heartbeat and Timing Parameters

| Parameter | Value | Default | Purpose |
|-----------|-------|---------|---------|
| `HeartbeatInterval` | 700ms | 1s | Faster mesh maintenance cycles |
| `OpportunisticGraftTicks` | 30 | 60 | Check for better peers every 30 heartbeats |
| `OpportunisticGraftPeers` | 4 | 2 | Graft up to 4 peers opportunistically |
| `PruneBackoff` | 5 min | 1 min | Longer backoff prevents repeated pruning |
| `UnsubscribeBackoff` | 30s | 10s | Backoff after unsubscribing from topic |
| `ConnectionTimeout` | 60s | 30s | Longer timeout for connections |
| `DirectConnectTicks` | 300 | 300 | Check direct connections every 300 heartbeats |
| `DirectConnectInitialDelay` | 1s | 1s | Initial delay before direct connects |
| `FanoutTTL` | 60s | 60s | How long to keep peers in fanout |
| `MaxPendingConnections` | 256 | 128 | More pending connections allowed |
| `HistoryLength` | 12 | 5 | Keep more message IDs in cache |
| `HistoryGossip` | 6 | 3 | Gossip more history entries |
| `GossipFactor` | 0.25 | 0.25 | Standard gossip emission factor |

### Implementation Notes
```go
// Import the gossipconfig package
import "github.com/powerloom/snapshot-sequencer-validator/pkgs/gossipconfig"

// Use the standardized configuration
gossipParams, peerScoreParams, peerScoreThresholds := gossipconfig.ConfigureSnapshotSubmissionsMesh(host.ID())

// Create pubsub with these parameters
ps, err := pubsub.NewGossipSub(
    ctx,
    host,
    pubsub.WithGossipSubParams(gossipParams),
    pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
    pubsub.WithFloodPublish(true),
)
```

## Mesh Delivery Penalties (DISABLED)

The most critical change: **completely disabling mesh delivery penalties** that cause pruning.

```go
// CRITICAL: These are set to 0 to prevent penalty-based pruning
MeshMessageDeliveriesWeight:     0.0,             // NO PENALTY
MeshMessageDeliveriesActivation: 24 * time.Hour,  // Never activate
MeshMessageDeliveriesDecay:      0.999,           // Extremely slow decay if ever enabled
MeshMessageDeliveriesThreshold:  0.001,           // Extremely low threshold
MeshMessageDeliveriesCap:        1.0,             // Minimal cap
MeshMessageDeliveriesWindow:     24 * time.Hour,  // Huge window
MeshFailurePenaltyWeight:        0.0,             // No penalty for mesh failures
```

### Why Disable?
In a network where some nodes (sequencers) primarily consume rather than relay:
- Default scoring penalizes non-relaying nodes
- Causes aggressive pruning of "passive" subscribers
- Results in network fragmentation

## Active Heartbeat Publishing Strategy

To maintain mesh membership, nodes publish periodic heartbeats. The local collector uses **two different heartbeat formats** optimized for each topic:

### Heartbeat Message Types

#### Type 1: Discovery Topic Heartbeat (`/powerloom/{prefix}/snapshot-submissions/0`)

**Format:**
```json
{
  "epoch_id": 0,
  "submissions": null,
  "snapshotter_id": "...",
  "signature": "heartbeat-discovery-..."
}
```

**DSV Node Handling:**
- **Detection**: Checks `epoch_id == 0 && submissions == null` (unified/main.go line 731)
- **Action**: Skipped immediately at queue level (no processing overhead)
- **Purpose**: Lightweight presence announcement for peer discovery

#### Type 2: Submissions Topic Heartbeat (`/powerloom/{prefix}/snapshot-submissions/all`)

**Format:**
```json
{
  "epoch_id": 0,
  "submissions": [{
    "request": {
      "epoch_id": 0,
      "project_id": "test:mesh-formation:local-collector",
      "snapshot_cid": ""
    }
  }],
  "snapshotter_id": "...",
  "signature": "heartbeat-submissions-..."
}
```

**DSV Node Handling:**
- **Detection**: Checks `EpochId == 0 && SnapshotCid == ""` (dequeuer.go line 229)
- **Action**: Queued but skipped during validation (recognized as heartbeat)
- **Error Handling**: Logged as debug, not error (unified/main.go line 1023)
- **Purpose**: Helps mesh formation on submissions topic while maintaining presence

### Why Two Types?

- **Discovery Topic**: Uses `null` submissions for minimal overhead - DSV skips queueing entirely
- **Submissions Topic**: Uses non-nil array with empty CID - helps mesh formation while still being recognized as heartbeat

Both types ensure nodes appear "active" even when not relaying application messages, preventing mesh pruning due to inactivity.

## Relevant Research & References

### libp2p Documentation
1. [Gossipsub v1.1 Specification](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md)
2. [Peer Scoring in Gossipsub](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md#peer-scoring)
3. [Episub: Proximity-aware Epidemic PubSub](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/episub.md)

### Go Implementation (v0.14.2)
1. [go-libp2p-pubsub parameters](https://github.com/libp2p/go-libp2p-pubsub/blob/v0.14.2/gossipsub.go)
2. [Score parameters](https://github.com/libp2p/go-libp2p-pubsub/blob/v0.14.2/score_params.go)
3. [Gossipsub tracer](https://github.com/libp2p/go-libp2p-pubsub/blob/v0.14.2/gossipsub_tracer.go)

### Academic Papers
1. [GossipSub: Attack-Resilient Message Propagation in the Filecoin and ETH2.0 Networks](https://arxiv.org/abs/2007.02754)
2. [Gossip-based Protocols for Large-scale Distributed Systems](https://www.cs.cornell.edu/home/rvr/papers/GossipFD.pdf)

### Debugging Tools
1. [pubsub-tracer](https://github.com/libp2p/go-libp2p-pubsub/tree/master/tracer) - Built-in tracing
2. [libp2p-introspection](https://github.com/libp2p/go-libp2p/tree/master/p2p/host/peerstore) - Peer store analysis
3. Our custom [P2P debugger tool](../libp2p-gossipsub-topic-debugger/) - Network diagnostics

## Common Issues and Solutions

### Issue: Nodes Being Pruned from Mesh
**Symptoms**: Peer count drops to 0-2, messages stop arriving
**Solution**: Implement the parameters above + active heartbeat

### Issue: Slow Message Propagation
**Symptoms**: High latency between publish and receive
**Solution**: Decrease HeartbeatInterval, increase D parameters

### Issue: Network Partition
**Symptoms**: Separate clusters of nodes not communicating
**Solution**: Ensure sufficient bootstrap nodes, check NAT traversal

## Testing & Validation

### Metrics to Monitor
1. **Mesh peer count**: Should stay between Dlo and Dhi
2. **Message delivery rate**: >95% expected
3. **Prune events**: Should be minimal with these settings
4. **Graft events**: Should stabilize after initial mesh formation

### Test Commands
```bash
# Monitor mesh status
curl http://localhost:8001/metrics | grep mesh

# Check peer connections
curl http://localhost:8001/debug/peers

# Trace gossipsub events (if enabled)
tail -f gossipsub_trace.json | jq '.event_type'
```

## Implementation Checklist

When implementing these parameters:

- [ ] Ensure using go-libp2p v0.43.0 and go-libp2p-pubsub v0.14.2
- [ ] Import standardized `gossipconfig` package
- [ ] Set all D-parameters consistently
- [ ] Disable mesh delivery penalties (set weights to 0)
- [ ] Configure extremely lenient peer score thresholds
- [ ] Implement heartbeat publishing for active presence
- [ ] Configure proper bootstrap nodes
- [ ] Enable flood publishing for critical messages
- [ ] Set up monitoring/metrics
- [ ] Test with multiple node configurations
- [ ] Document any environment-specific adjustments

## Further Reading

### Gossipsub Design Decisions
- [Why Gossipsub?](https://blog.libp2p.io/2020-05-20-gossipsub-v1.1/)
- [Hardening Gossipsub](https://blog.libp2p.io/gossipsub-hardening/)
- [Gossipsub Security](https://research.protocol.ai/publications/gossipsub-attack-resilient-message-propagation-in-the-filecoin-and-eth2.0-networks/)

### Alternative Approaches
- [Epidemic Broadcast Trees](http://www.gsd.inesc-id.pt/~ler/reports/srds07.pdf)
- [HyParView](https://asc.di.fct.unl.pt/~jleitao/pdf/dsn07-leitao.pdf)
- [Plumtree Protocol](http://www.gsd.inesc-id.pt/~ler/reports/srds07.pdf)

## Peer Scoring Implementation Priority

### Key Insight: Subscribers Control the Mesh

In gossipsub, **subscribers are the gatekeepers** - they decide who stays in their mesh. Publishers can't force themselves into a subscriber's mesh; they can only avoid being pruned by meeting the subscriber's scoring criteria.

### The Mesh Control Dynamic
```
Publisher → [Wants to send] → Subscriber's Mesh
                ↑
        Subscriber decides: Accept or Prune?
        (Based on SUBSCRIBER's scoring config)
```

- **Each peer sets its own scoring parameters locally** - there's no network-wide consensus
- **Scores are computed locally** based on observed behavior
- **Asymmetric relationship**: Your scoring config controls how YOU treat others, not how they treat you
- **Disabling penalties locally** means YOU won't kick peers out, but they can still kick YOU out

### Implementation Priority Order

#### Priority 1: SUBSCRIBERS (They Control Mesh Membership)
These components MUST have proper scoring configuration:

1. **Unified Sequencer** (Subscriber) ❓ CHECK NEEDED
   - Subscribes to snapshot submission topics
   - Needs lenient scoring to keep publishers in mesh

2. **P2P Debugger - LISTENER Mode** ❌ NEEDS IMPLEMENTATION
   - Critical for testing subscriber behavior
   - Should have lenient scoring to keep publishers

3. **P2P Debugger - DISCOVERY Mode** ❌ NEEDS IMPLEMENTATION  
   - Monitors discovery topic
   - Should have lenient scoring to observe all peers

#### Priority 2: PUBLISHERS (They Need Anti-Pruning)
These need anti-pruning parameters to stay in mesh:

1. **Local Collector** (`snapshotter-lite-local-collector`) ✅ IMPLEMENTED
   - Location: `pkgs/service/initialization.go`
   - PUBLISHER that broadcasts snapshot submissions
   - Has anti-pruning parameters + heartbeat to avoid being pruned

2. **P2P Debugger - PUBLISHER Mode** ❌ NEEDS IMPLEMENTATION
   - Should mirror local collector's anti-pruning strategy

### Current Implementation Status

| Component | Role | Has Scoring? | Priority | Impact if Missing |
|-----------|------|--------------|----------|------------------|
| Local Collector | **Publisher** | ✅ Yes (v0.43.0) | - | Has anti-pruning params |
| Unified Sequencer | **Subscriber** | ✅ Yes (v0.43.0) | - | Using gossipconfig |
| P2P Debugger | **Various** | ✅ Yes (v0.43.0) | - | Using gossipconfig |
| Bootstrap Node | **Infrastructure** | ✅ Yes (v0.43.0) | - | Updated to latest |

### Why This Matters

The gossipsub design philosophy is "trust no one, score everyone based on what YOU observe". This means:
- Different peers can have completely different scoring configurations
- A peer might heavily penalize another, while that peer doesn't penalize back
- The network remains resilient but requires careful tuning for asymmetric use cases

## Contact & Support

For questions about these parameters:
1. Check [libp2p Discord](https://discord.gg/libp2p)
2. Open issue on [go-libp2p-pubsub](https://github.com/libp2p/go-libp2p-pubsub/issues)
3. Review our [P2P Network Setup Guide](./P2P_NETWORK_SETUP_GUIDE.md)