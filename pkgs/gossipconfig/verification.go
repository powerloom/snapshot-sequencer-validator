// Package gossipconfig provides parameter verification and monitoring for gossipsub configurations
package gossipconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ConfigVersion represents the version of the gossipsub configuration
// This should be incremented whenever breaking changes are made to parameters
const ConfigVersion = "v1.0.0"

// ProtocolIDWithVersion returns a versioned protocol ID for gossipsub
// This ensures only peers with compatible configurations can mesh together
func ProtocolIDWithVersion() protocol.ID {
	return protocol.ID(fmt.Sprintf("/meshsub/1.2.0/powerloom/%s", ConfigVersion))
}

// ConfigFingerprint represents a unique identifier for a gossipsub configuration
type ConfigFingerprint struct {
	Version    string    `json:"version"`
	Hash       string    `json:"hash"`
	Timestamp  time.Time `json:"timestamp"`
	Parameters ConfigSummary `json:"parameters"`
}

// ConfigSummary contains the key gossipsub parameters for verification
type ConfigSummary struct {
	// Mesh parameters
	D     int `json:"d"`
	Dlo   int `json:"dlo"`
	Dhi   int `json:"dhi"`
	Dlazy int `json:"dlazy"`
	Dout  int `json:"dout"`
	Dscore int `json:"dscore"`
	
	// Timing parameters (in milliseconds for consistency)
	HeartbeatIntervalMs int64 `json:"heartbeat_interval_ms"`
	PruneBackoffMs      int64 `json:"prune_backoff_ms"`
	
	// History parameters
	HistoryLength int `json:"history_length"`
	HistoryGossip int `json:"history_gossip"`
	
	// Scoring thresholds (simplified for verification)
	GossipThreshold              float64 `json:"gossip_threshold"`
	PublishThreshold             float64 `json:"publish_threshold"`
	GraylistThreshold            float64 `json:"graylist_threshold"`
	AcceptPXThreshold            float64 `json:"accept_px_threshold"`
	OpportunisticGraftThreshold  float64 `json:"opportunistic_graft_threshold"`
	
	// Feature flags
	FloodPublish bool `json:"flood_publish"`
	
	// Topic-specific parameters
	TopicWeight                   float64 `json:"topic_weight"`
	TimeInMeshWeight              float64 `json:"time_in_mesh_weight"`
	FirstMessageDeliveriesWeight  float64 `json:"first_message_deliveries_weight"`
	MeshMessageDeliveriesWeight   float64 `json:"mesh_message_deliveries_weight"`
	MeshFailurePenaltyWeight      float64 `json:"mesh_failure_penalty_weight"`
}

// GenerateConfigFingerprint creates a unique fingerprint for the current configuration
func GenerateConfigFingerprint(params *pubsub.GossipSubParams, scoreParams *pubsub.PeerScoreParams, thresholds *pubsub.PeerScoreThresholds, floodPublish bool) (*ConfigFingerprint, error) {
	// Extract key parameters into a summary
	summary := ConfigSummary{
		// Mesh parameters
		D:     params.D,
		Dlo:   params.Dlo,
		Dhi:   params.Dhi,
		Dlazy: params.Dlazy,
		Dout:  params.Dout,
		Dscore: params.Dscore,
		
		// Timing parameters
		HeartbeatIntervalMs: params.HeartbeatInterval.Milliseconds(),
		PruneBackoffMs:      params.PruneBackoff.Milliseconds(),
		
		// History parameters
		HistoryLength: params.HistoryLength,
		HistoryGossip: params.HistoryGossip,
		
		// Scoring thresholds
		GossipThreshold:              thresholds.GossipThreshold,
		PublishThreshold:             thresholds.PublishThreshold,
		GraylistThreshold:            thresholds.GraylistThreshold,
		AcceptPXThreshold:            thresholds.AcceptPXThreshold,
		OpportunisticGraftThreshold:  thresholds.OpportunisticGraftThreshold,
		
		// Feature flags
		FloodPublish: floodPublish,
	}
	
	// Add topic-specific parameters if available
	if topicParams, ok := scoreParams.Topics["/powerloom/snapshot-submissions/all"]; ok {
		summary.TopicWeight = topicParams.TopicWeight
		summary.TimeInMeshWeight = topicParams.TimeInMeshWeight
		summary.FirstMessageDeliveriesWeight = topicParams.FirstMessageDeliveriesWeight
		summary.MeshMessageDeliveriesWeight = topicParams.MeshMessageDeliveriesWeight
		summary.MeshFailurePenaltyWeight = topicParams.MeshFailurePenaltyWeight
	}
	
	// Generate hash of the configuration
	configJSON, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config summary: %w", err)
	}
	
	hash := sha256.Sum256(configJSON)
	hashStr := hex.EncodeToString(hash[:])
	
	return &ConfigFingerprint{
		Version:    ConfigVersion,
		Hash:       hashStr[:16], // Use first 16 chars for brevity
		Timestamp:  time.Now(),
		Parameters: summary,
	}, nil
}

// ParameterVerificationTracer implements pubsub.EventTracer to monitor mesh health and parameter compatibility
type ParameterVerificationTracer struct {
	mu                  sync.RWMutex
	localFingerprint    *ConfigFingerprint
	peerFingerprints    map[peer.ID]*ConfigFingerprint
	meshHealth          map[string]*MeshHealth
	incompatiblePeers   map[peer.ID]string // peer ID -> reason for incompatibility
	parameterMismatches []ParameterMismatch
}

// MeshHealth tracks the health of a gossipsub mesh
type MeshHealth struct {
	Topic            string    `json:"topic"`
	MeshPeers        int       `json:"mesh_peers"`
	FanoutPeers      int       `json:"fanout_peers"`
	LastUpdate       time.Time `json:"last_update"`
	MessagesSent     int64     `json:"messages_sent"`
	MessagesReceived int64     `json:"messages_received"`
	PruneEvents      int64     `json:"prune_events"`
	GraftEvents      int64     `json:"graft_events"`
}

// ParameterMismatch records a parameter difference between peers
type ParameterMismatch struct {
	PeerID    peer.ID   `json:"peer_id"`
	Parameter string    `json:"parameter"`
	LocalValue interface{} `json:"local_value"`
	PeerValue  interface{} `json:"peer_value"`
	Timestamp  time.Time `json:"timestamp"`
}

// NewParameterVerificationTracer creates a new tracer for monitoring gossipsub parameters
func NewParameterVerificationTracer(localFingerprint *ConfigFingerprint) *ParameterVerificationTracer {
	return &ParameterVerificationTracer{
		localFingerprint:    localFingerprint,
		peerFingerprints:    make(map[peer.ID]*ConfigFingerprint),
		meshHealth:          make(map[string]*MeshHealth),
		incompatiblePeers:   make(map[peer.ID]string),
		parameterMismatches: make([]ParameterMismatch, 0),
	}
}

// Trace implements the EventTracer interface
func (t *ParameterVerificationTracer) Trace(evt *pubsub.Evt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	switch evt.Type {
	case pubsub.PeerJoin:
		// Track when peers join topics
		if evt.Topic != nil {
			topic := *evt.Topic
			if _, ok := t.meshHealth[topic]; !ok {
				t.meshHealth[topic] = &MeshHealth{
					Topic:      topic,
					LastUpdate: time.Now(),
				}
			}
			t.meshHealth[topic].MeshPeers++
			t.meshHealth[topic].LastUpdate = time.Now()
		}
		
	case pubsub.PeerLeave:
		// Track when peers leave topics
		if evt.Topic != nil {
			topic := *evt.Topic
			if health, ok := t.meshHealth[topic]; ok {
				health.MeshPeers--
				health.LastUpdate = time.Now()
			}
		}
		
	case pubsub.Graft:
		// Track graft events (peer added to mesh)
		if evt.Topic != nil {
			topic := *evt.Topic
			if health, ok := t.meshHealth[topic]; ok {
				health.GraftEvents++
				health.LastUpdate = time.Now()
			}
		}
		
	case pubsub.Prune:
		// Track prune events (peer removed from mesh)
		if evt.Topic != nil {
			topic := *evt.Topic
			if health, ok := t.meshHealth[topic]; ok {
				health.PruneEvents++
				health.LastUpdate = time.Now()
			}
		}
		
	case pubsub.DeliverMessage:
		// Track message delivery
		if evt.Topic != nil {
			topic := *evt.Topic
			if health, ok := t.meshHealth[topic]; ok {
				health.MessagesReceived++
				health.LastUpdate = time.Now()
			}
		}
		
	case pubsub.PublishMessage:
		// Track message publishing
		if evt.Topic != nil {
			topic := *evt.Topic
			if health, ok := t.meshHealth[topic]; ok {
				health.MessagesSent++
				health.LastUpdate = time.Now()
			}
		}
	}
}

// GetMeshHealth returns the current mesh health statistics
func (t *ParameterVerificationTracer) GetMeshHealth() map[string]*MeshHealth {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Create a copy to avoid race conditions
	result := make(map[string]*MeshHealth)
	for k, v := range t.meshHealth {
		healthCopy := *v
		result[k] = &healthCopy
	}
	return result
}

// GetIncompatiblePeers returns peers with incompatible configurations
func (t *ParameterVerificationTracer) GetIncompatiblePeers() map[peer.ID]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Create a copy
	result := make(map[peer.ID]string)
	for k, v := range t.incompatiblePeers {
		result[k] = v
	}
	return result
}

// GetParameterMismatches returns detected parameter mismatches
func (t *ParameterVerificationTracer) GetParameterMismatches() []ParameterMismatch {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Return a copy
	result := make([]ParameterMismatch, len(t.parameterMismatches))
	copy(result, t.parameterMismatches)
	return result
}

// VerifyPeerConfiguration checks if a peer's configuration is compatible
func (t *ParameterVerificationTracer) VerifyPeerConfiguration(peerID peer.ID, peerFingerprint *ConfigFingerprint) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Store peer fingerprint
	t.peerFingerprints[peerID] = peerFingerprint
	
	// Check version compatibility
	if peerFingerprint.Version != t.localFingerprint.Version {
		t.incompatiblePeers[peerID] = fmt.Sprintf("version mismatch: local=%s, peer=%s", 
			t.localFingerprint.Version, peerFingerprint.Version)
		return false
	}
	
	// Check critical parameters
	if !t.compareParameters(peerID, peerFingerprint.Parameters) {
		return false
	}
	
	// Remove from incompatible list if previously marked
	delete(t.incompatiblePeers, peerID)
	return true
}

// compareParameters compares critical parameters between local and peer configurations
func (t *ParameterVerificationTracer) compareParameters(peerID peer.ID, peerParams ConfigSummary) bool {
	localParams := t.localFingerprint.Parameters
	compatible := true
	
	// Check mesh parameters (these should match exactly)
	if peerParams.D != localParams.D {
		t.recordMismatch(peerID, "D", localParams.D, peerParams.D)
		compatible = false
	}
	if peerParams.Dlo != localParams.Dlo {
		t.recordMismatch(peerID, "Dlo", localParams.Dlo, peerParams.Dlo)
		compatible = false
	}
	if peerParams.Dhi != localParams.Dhi {
		t.recordMismatch(peerID, "Dhi", localParams.Dhi, peerParams.Dhi)
		compatible = false
	}
	
	// Check heartbeat interval (allow small variance)
	heartbeatDiff := abs(peerParams.HeartbeatIntervalMs - localParams.HeartbeatIntervalMs)
	if heartbeatDiff > 100 { // Allow 100ms variance
		t.recordMismatch(peerID, "HeartbeatInterval", localParams.HeartbeatIntervalMs, peerParams.HeartbeatIntervalMs)
		compatible = false
	}
	
	// Check flood publish setting (critical for message propagation)
	if peerParams.FloodPublish != localParams.FloodPublish {
		t.recordMismatch(peerID, "FloodPublish", localParams.FloodPublish, peerParams.FloodPublish)
		compatible = false
	}
	
	// Check scoring weights (allow some variance for non-critical parameters)
	if peerParams.MeshMessageDeliveriesWeight != localParams.MeshMessageDeliveriesWeight {
		// This is critical - if one peer has penalties enabled and another doesn't
		t.recordMismatch(peerID, "MeshMessageDeliveriesWeight", localParams.MeshMessageDeliveriesWeight, peerParams.MeshMessageDeliveriesWeight)
		// Warning but not incompatible
	}
	
	if !compatible {
		t.incompatiblePeers[peerID] = "parameter mismatches detected"
	}
	
	return compatible
}

// recordMismatch records a parameter mismatch for debugging
func (t *ParameterVerificationTracer) recordMismatch(peerID peer.ID, param string, localValue, peerValue interface{}) {
	mismatch := ParameterMismatch{
		PeerID:     peerID,
		Parameter:  param,
		LocalValue: localValue,
		PeerValue:  peerValue,
		Timestamp:  time.Now(),
	}
	
	t.parameterMismatches = append(t.parameterMismatches, mismatch)
	
	// Keep only last 100 mismatches to avoid memory growth
	if len(t.parameterMismatches) > 100 {
		t.parameterMismatches = t.parameterMismatches[len(t.parameterMismatches)-100:]
	}
}

// abs returns the absolute value of an int64
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// CreateGossipSubWithVerification creates a gossipsub instance with parameter verification
func CreateGossipSubWithVerification(ctx interface{}, host interface{}, opts ...pubsub.Option) (*pubsub.PubSub, *ParameterVerificationTracer, error) {
	// Get standard configuration
	gossipParams, peerScoreParams, peerScoreThresholds := ConfigureSnapshotSubmissionsMesh(host.(interface{ ID() peer.ID }).ID())
	
	// Generate fingerprint for our configuration
	fingerprint, err := GenerateConfigFingerprint(gossipParams, peerScoreParams, peerScoreThresholds, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate config fingerprint: %w", err)
	}
	
	// Create verification tracer
	tracer := NewParameterVerificationTracer(fingerprint)
	
	// Add our options
	allOpts := []pubsub.Option{
		pubsub.WithGossipSubParams(*gossipParams),
		pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
		pubsub.WithFloodPublish(true),
		pubsub.WithEventTracer(tracer),
		// Use versioned protocol ID to ensure compatibility
		pubsub.WithGossipSubProtocols(
			[]protocol.ID{ProtocolIDWithVersion(), pubsub.GossipSubID_v12, pubsub.GossipSubID_v11},
			func(feat pubsub.GossipSubFeature, proto protocol.ID) bool {
				// Accept all features for our versioned protocol
				if proto == ProtocolIDWithVersion() {
					return true
				}
				// Use default for standard protocols
				return false
			},
		),
	}
	
	// Append user-provided options
	allOpts = append(allOpts, opts...)
	
	// Create gossipsub
	ps, err := pubsub.NewGossipSub(ctx.(interface{}).(*interface{}), host.(interface{}), allOpts...)
	if err != nil {
		return nil, nil, err
	}
	
	return ps, tracer, nil
}