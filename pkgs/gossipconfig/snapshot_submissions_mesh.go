package gossipconfig

import (
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// SnapshotSubmissionsGossipParams returns the standard gossipsub parameters
// that should be used by ALL peers participating in the snapshot submissions mesh.
// These parameters ensure mesh stability and prevent unnecessary pruning
// for the snapshot submission topics: /powerloom/snapshot-submissions/*
func SnapshotSubmissionsGossipParams() *pubsub.GossipSubParams {
	params := &pubsub.GossipSubParams{
		// CRITICAL: More aggressive mesh maintenance parameters for snapshot submissions
		D:     10, // Higher target mesh degree (default 6)
		Dlo:   8,  // Higher lower bound (default 5)
		Dhi:   16, // Higher upper bound (default 12)
		Dlazy: 10, // Higher lazy push threshold (default 6)
		Dout:  4,  // Outbound connections per topic (default 2)
		Dscore: 6, // Score-based peer selection (default 4)

		// Faster heartbeat for more responsive mesh maintenance
		HeartbeatInterval: 700 * time.Millisecond, // Much faster (default 1s)

		// History parameters for better message propagation
		HistoryLength: 12, // Keep more message IDs (default 5)
		HistoryGossip: 6,  // Gossip more history (default 3)

		// Gossip factor - controls gossip emission
		GossipFactor: 0.25, // Standard gossip factor

		// Opportunistic grafting for better mesh formation
		OpportunisticGraftTicks:  30, // Check every 30 heartbeats (default 60)
		OpportunisticGraftPeers:  4,  // Graft up to 4 peers (default 2)
		OpportunisticGraftSamples: 10, // Sample 10 peers (default)

		// Prune backoff to prevent aggressive pruning
		PruneBackoff:        5 * time.Minute, // Longer backoff (default 1 min)
		UnsubscribeBackoff:  30 * time.Second, // Unsubscribe backoff
		ConnectorBackoff:    30 * time.Second, // Connector backoff

		// Connection management
		MaxPendingConnections: 256,             // More pending connections
		ConnectionTimeout:     60 * time.Second, // Longer timeout

		// Direct connect parameters
		DirectConnectTicks:   300,              // Check direct connections (default 300)
		DirectConnectInitialDelay: 1 * time.Second, // Initial delay

		// Fanout TTL - how long to keep peers in fanout
		FanoutTTL: 60 * time.Second, // Standard fanout TTL
	}

	return params
}

// SnapshotSubmissionsPeerScoreParams returns the peer scoring parameters
// for the snapshot submissions mesh with mesh delivery penalties DISABLED 
// to prevent pruning of low-activity peers.
func SnapshotSubmissionsPeerScoreParams(hostID peer.ID) *pubsub.PeerScoreParams {
	return &pubsub.PeerScoreParams{
		// CRITICAL: Very high baseline positive scores
		AppSpecificScore: func(p peer.ID) float64 {
			// Give ourselves an extremely high score
			if p == hostID {
				return 100000.0 // Extremely high self-score
			}
			// Give all other peers a very high baseline
			return 10000.0 // Very high baseline for all peers
		},
		AppSpecificWeight: 10.0, // Higher weight for app-specific scores

		// CRITICAL: Disable IP colocation penalties completely
		IPColocationFactorThreshold: 1000, // Effectively disabled
		IPColocationFactorWeight:    0.0,  // No penalty
		IPColocationFactorWhitelist:  nil,

		// CRITICAL: Completely disable behavior penalties
		BehaviourPenaltyWeight:    0.0,     // NO penalty (disabled)
		BehaviourPenaltyThreshold: 1000.0,  // Very high threshold
		BehaviourPenaltyDecay:     0.999,   // Extremely slow decay

		// Remove time in mesh penalty for new peers
		RetainScore: 30 * time.Minute, // Keep scores longer

		// Topics will be set per implementation
		Topics: make(map[string]*pubsub.TopicScoreParams),
	}
}

// SnapshotSubmissionsTopicScoreParams returns topic scoring parameters
// for snapshot submission topics with mesh delivery penalties DISABLED.
func SnapshotSubmissionsTopicScoreParams() *pubsub.TopicScoreParams {
	return &pubsub.TopicScoreParams{
		// CRITICAL: High topic weight for positive scores
		TopicWeight: 10.0, // High topic importance for positive scoring

		// CRITICAL: Strongly reward staying in mesh
		TimeInMeshWeight:  10.0,            // High positive weight
		TimeInMeshQuantum: 1 * time.Second, // Fast accumulation
		TimeInMeshCap:     10000.0,         // Very high cap

		// CRITICAL: Strongly reward publishing
		FirstMessageDeliveriesWeight: 100.0,   // Very high positive weight
		FirstMessageDeliveriesDecay:  0.999,   // Extremely slow decay
		FirstMessageDeliveriesCap:    10000.0, // Very high cap

		// CRITICAL: COMPLETELY DISABLE mesh delivery penalties
		MeshMessageDeliveriesWeight:     0.0,           // NO PENALTY (disabled)
		MeshMessageDeliveriesDecay:      0.999,         // Extremely slow decay if ever enabled
		MeshMessageDeliveriesThreshold:  0.001,         // Extremely low threshold
		MeshMessageDeliveriesCap:        1.0,           // Minimal cap
		MeshMessageDeliveriesActivation: 24 * time.Hour, // Never activate in practice
		MeshMessageDeliveriesWindow:     24 * time.Hour, // Huge window

		// CRITICAL: Disable ALL failure penalties
		MeshFailurePenaltyWeight: 0.0, // No penalty for mesh failures
		MeshFailurePenaltyDecay:  0.0,

		// Invalid messages - keep standard penalties
		InvalidMessageDeliveriesWeight: -100.0,
		InvalidMessageDeliveriesDecay:  0.5,
	}
}

// SnapshotSubmissionsPeerScoreThresholds returns extremely lenient thresholds
// for the snapshot submissions mesh to prevent aggressive pruning.
func SnapshotSubmissionsPeerScoreThresholds() *pubsub.PeerScoreThresholds {
	return &pubsub.PeerScoreThresholds{
		GossipThreshold:             -100000, // Extremely lenient (default -10)
		PublishThreshold:            -200000, // Extremely lenient (default -50)
		GraylistThreshold:           -500000, // Extremely lenient (default -80)
		AcceptPXThreshold:           -10000,  // Accept peer exchange even from negative peers
		OpportunisticGraftThreshold: -1000,   // Graft even slightly negative peers
	}
}

// ConfigureSnapshotSubmissionsMesh configures a pubsub instance with parameters
// optimized for the snapshot submissions mesh. This should be called by all nodes
// participating in snapshot submission topics.
func ConfigureSnapshotSubmissionsMesh(hostID peer.ID) (*pubsub.GossipSubParams, *pubsub.PeerScoreParams, *pubsub.PeerScoreThresholds) {
	// Get snapshot submissions mesh parameters
	gossipParams := SnapshotSubmissionsGossipParams()
	peerScoreParams := SnapshotSubmissionsPeerScoreParams(hostID)
	peerScoreThresholds := SnapshotSubmissionsPeerScoreThresholds()

	// Configure topic score parameters for snapshot submission topics
	topicScoreParams := SnapshotSubmissionsTopicScoreParams()
	
	// Set scoring for both snapshot submission topics
	peerScoreParams.Topics["/powerloom/snapshot-submissions/0"] = topicScoreParams   // Discovery topic
	peerScoreParams.Topics["/powerloom/snapshot-submissions/all"] = topicScoreParams // Submissions topic

	return gossipParams, peerScoreParams, peerScoreThresholds
}

// Example usage in your application:
//
// gossipParams, peerScoreParams, peerScoreThresholds := gossipconfig.ConfigureSnapshotSubmissionsMesh(host.ID())
// 
// ps, err := pubsub.NewGossipSub(
//     ctx,
//     host,
//     pubsub.WithGossipSubParams(gossipParams),
//     pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
//     pubsub.WithFloodPublish(true),
// )