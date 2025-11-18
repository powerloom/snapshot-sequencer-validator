// Package gossipconfig provides standardized gossipsub configuration for the
// snapshot submission network. All parameters have been carefully tuned to:
// 1. Prevent aggressive pruning in asymmetric publisher/subscriber scenarios
// 2. Maintain mesh stability with high positive peer scores
// 3. Disable penalties that could disconnect low-activity peers
// 4. Pass all validation requirements of go-libp2p-pubsub v0.14.2
//
// IMPORTANT: These parameters are validated against go-libp2p-pubsub v0.14.2
// validation rules. Each parameter includes comments about its validation
// requirements to prevent future breaking changes.
package gossipconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

		// Prune backoff to prevent aggressive pruning
		PruneBackoff:        5 * time.Minute, // Longer backoff (default 1 min)
		UnsubscribeBackoff:  30 * time.Second, // Unsubscribe backoff

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

		// Topic score cap - limits total positive contribution from topics
		// Set high to allow topics to contribute significantly to positive scores
		TopicScoreCap: 100000.0, // Very high cap to allow topic scores to accumulate

		// CRITICAL: Disable IP colocation penalties completely
		IPColocationFactorThreshold: 1000, // Effectively disabled (high threshold)
		IPColocationFactorWeight:    0.0,  // No penalty (weight = 0)
		IPColocationFactorWhitelist:  nil,

		// CRITICAL: Completely disable behavior penalties
		BehaviourPenaltyWeight:    0.0,    // NO penalty (disabled with weight = 0)
		BehaviourPenaltyThreshold: 1000.0, // Very high threshold (but irrelevant since weight = 0)
		BehaviourPenaltyDecay:     0.999,  // Extremely slow decay (but irrelevant since weight = 0)

		// Score decay parameters (REQUIRED - validation will fail without these)
		DecayInterval: 1 * time.Second, // Required: minimum 1s
		DecayToZero:   0.01,             // Required: between 0 and 1, slow decay to zero

		// Time to retain scores for disconnected peers
		RetainScore: 30 * time.Minute, // Keep scores longer

		// Message TTL - 0 means use global default
		SeenMsgTTL: 0, // Use global TimeCacheDuration

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

		// P1: Time in mesh - strongly reward staying in mesh
		TimeInMeshWeight:  10.0,            // High positive weight (must be >= 0)
		TimeInMeshQuantum: 1 * time.Second, // Fast accumulation (must be > 0)
		TimeInMeshCap:     10000.0,         // Very high cap (must be > 0)

		// P2: First message deliveries - strongly reward publishing
		FirstMessageDeliveriesWeight: 100.0, // Very high positive weight (must be >= 0)
		FirstMessageDeliveriesDecay:  0.999, // Extremely slow decay (must be between 0 and 1)
		FirstMessageDeliveriesCap:    10000.0, // Very high cap (must be > 0)

		// P3: Mesh message deliveries - COMPLETELY DISABLED
		// Setting weight to 0 disables this penalty entirely
		MeshMessageDeliveriesWeight:     0.0,            // NO PENALTY (disabled with weight = 0)
		MeshMessageDeliveriesDecay:      0.999,          // Decay between 0 and 1 (required even if disabled)
		MeshMessageDeliveriesThreshold:  1.0,            // Must be > 0 (but irrelevant since weight = 0)
		MeshMessageDeliveriesCap:        1.0,            // Must be > 0 (but irrelevant since weight = 0)
		MeshMessageDeliveriesActivation: 24 * time.Hour, // Must be >= 1s (set high to never activate)
		MeshMessageDeliveriesWindow:     24 * time.Hour, // Must be >= 0 (huge window)

		// P3b: Mesh failure penalty - COMPLETELY DISABLED
		// Setting weight to 0 disables this penalty entirely
		MeshFailurePenaltyWeight: 0.0, // No penalty for mesh failures (weight = 0 disables)
		MeshFailurePenaltyDecay:  0.9, // Must be between 0 and 1 (even if disabled)

		// P4: Invalid messages - keep standard penalties
		// These parameters penalize peers that send invalid messages
		InvalidMessageDeliveriesWeight: -100.0, // Negative weight for penalties (must be <= 0)
		InvalidMessageDeliveriesDecay:  0.5,    // Moderate decay (must be between 0 and 1)
	}
}

// SnapshotSubmissionsPeerScoreThresholds returns extremely lenient thresholds
// for the snapshot submissions mesh to prevent aggressive pruning.
func SnapshotSubmissionsPeerScoreThresholds() *pubsub.PeerScoreThresholds {
	return &pubsub.PeerScoreThresholds{
		// Gossip threshold - score below which gossip is suppressed
		// Must be <= 0. Set very low to be extremely lenient
		GossipThreshold: -100000, // Extremely lenient (default -10)
		
		// Publish threshold - score below which we don't publish to peer
		// Must be <= 0 and <= GossipThreshold
		PublishThreshold: -200000, // Extremely lenient (default -50)
		
		// Graylist threshold - score below which peer is graylisted
		// Must be <= 0 and <= PublishThreshold
		GraylistThreshold: -500000, // Extremely lenient (default -80)
		
		// Accept PX threshold - score threshold for accepting peer exchange
		// Must be >= 0. Set to 0 to accept all peer exchanges
		AcceptPXThreshold: 0, // Accept all peer exchanges (must be >= 0)
		
		// Opportunistic graft threshold - median score before opportunistic grafting
		// Must be >= 0. Set to small positive value to enable opportunistic grafting
		// even for peers with slightly positive scores
		OpportunisticGraftThreshold: 1.0, // Small positive value (must be >= 0)
	}
}

// GenerateParamHash creates a deterministic hash of gossipsub parameters
// This hash can be used to verify all peers are using the same configuration
func GenerateParamHash(params *pubsub.GossipSubParams) string {
	// Create a deterministic string from key parameters
	paramStr := fmt.Sprintf("D:%d_Dlo:%d_Dhi:%d_Dlazy:%d_HB:%d_FB:%d_MC:%d_MIT:%d",
		params.D,
		params.Dlo,
		params.Dhi,
		params.Dlazy,
		params.HeartbeatInterval.Milliseconds(),
		int(params.FanoutTTL.Seconds()),
		params.MaxPendingConnections,
		params.MaxIHaveLength,
	)
	
	hash := sha256.Sum256([]byte(paramStr))
	return hex.EncodeToString(hash[:8]) // First 8 bytes for readability
}

// ConfigureSnapshotSubmissionsMesh configures a pubsub instance with parameters
// optimized for the snapshot submissions mesh. This should be called by all nodes
// participating in snapshot submission topics.
// Returns gossipsub params, peer score params, thresholds, and a parameter hash for verification.
func ConfigureSnapshotSubmissionsMesh(hostID peer.ID, discoveryTopic, submissionsTopic string) (*pubsub.GossipSubParams, *pubsub.PeerScoreParams, *pubsub.PeerScoreThresholds, string) {
	// Get snapshot submissions mesh parameters
	gossipParams := SnapshotSubmissionsGossipParams()
	peerScoreParams := SnapshotSubmissionsPeerScoreParams(hostID)
	peerScoreThresholds := SnapshotSubmissionsPeerScoreThresholds()

	// Generate parameter hash for verification
	paramHash := GenerateParamHash(gossipParams)

	// Configure topic score parameters for snapshot submission topics
	topicScoreParams := SnapshotSubmissionsTopicScoreParams()

	// Set scoring for both snapshot submission topics using configurable parameters
	peerScoreParams.Topics[discoveryTopic] = topicScoreParams   // Discovery topic
	peerScoreParams.Topics[submissionsTopic] = topicScoreParams // Submissions topic

	return gossipParams, peerScoreParams, peerScoreThresholds, paramHash
}

// Example usage in your application:
//
// gossipParams, peerScoreParams, peerScoreThresholds, paramHash := gossipconfig.ConfigureSnapshotSubmissionsMesh(host.ID())
// log.Infof("Gossipsub parameter hash: %s", paramHash)
// 
// ps, err := pubsub.NewGossipSub(
//     ctx,
//     host,
//     pubsub.WithGossipSubParams(*gossipParams), // Note: dereference the pointer
//     pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
//     pubsub.WithFloodPublish(true),
// )
//
// PARAMETER VALIDATION SUMMARY (go-libp2p-pubsub v0.14.2):
// 
// PeerScoreParams requirements:
// - AppSpecificScore: REQUIRED function (cannot be nil)
// - TopicScoreCap: Must be >= 0 (0 means no cap)
// - IPColocationFactorWeight: Must be <= 0 (0 disables, negative enables penalty)
// - IPColocationFactorThreshold: Must be >= 1 if weight != 0
// - BehaviourPenaltyWeight: Must be <= 0 (0 disables, negative enables penalty)
// - BehaviourPenaltyDecay: Must be between 0 and 1 if weight != 0
// - BehaviourPenaltyThreshold: Must be >= 0
// - DecayInterval: REQUIRED, must be >= 1 second
// - DecayToZero: REQUIRED, must be between 0 and 1
//
// TopicScoreParams requirements:
// - TopicWeight: Must be >= 0
// - TimeInMeshQuantum: Must be > 0 if TimeInMeshWeight != 0
// - TimeInMeshCap: Must be > 0 if TimeInMeshWeight != 0
// - TimeInMeshWeight: Must be >= 0 (0 disables)
// - FirstMessageDeliveriesWeight: Must be >= 0 (0 disables)
// - FirstMessageDeliveriesDecay: Must be between 0 and 1 if weight != 0
// - FirstMessageDeliveriesCap: Must be > 0 if weight != 0
// - MeshMessageDeliveriesWeight: Must be <= 0 (0 disables, negative enables penalty)
// - MeshMessageDeliveriesDecay: Must be between 0 and 1 if weight != 0
// - MeshMessageDeliveriesThreshold: Must be > 0 if weight != 0
// - MeshMessageDeliveriesCap: Must be > 0 if weight != 0
// - MeshMessageDeliveriesActivation: Must be >= 1s if weight != 0
// - MeshMessageDeliveriesWindow: Must be >= 0
// - MeshFailurePenaltyWeight: Must be <= 0 (0 disables, negative enables penalty)
// - MeshFailurePenaltyDecay: Must be between 0 and 1 if weight != 0
// - InvalidMessageDeliveriesWeight: Must be <= 0 (0 disables, negative enables penalty)
// - InvalidMessageDeliveriesDecay: Must be between 0 and 1 if weight != 0
//
// PeerScoreThresholds requirements:
// - GossipThreshold: Must be <= 0
// - PublishThreshold: Must be <= 0 and <= GossipThreshold
// - GraylistThreshold: Must be <= 0 and <= PublishThreshold
// - AcceptPXThreshold: Must be >= 0
// - OpportunisticGraftThreshold: Must be >= 0