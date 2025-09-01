// Package gossipconfig provides example integration for parameter verification
package gossipconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// ExampleIntegration shows how to integrate parameter verification into your P2P application
func ExampleIntegration(ctx context.Context, h host.Host) (*pubsub.PubSub, *ParameterVerificationTracer, *MonitoringService, error) {
	// Step 1: Get standard configuration
	gossipParams, peerScoreParams, peerScoreThresholds := ConfigureSnapshotSubmissionsMesh(h.ID())
	
	// Step 2: Generate configuration fingerprint
	fingerprint, err := GenerateConfigFingerprint(gossipParams, peerScoreParams, peerScoreThresholds, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate config fingerprint: %w", err)
	}
	
	// Step 3: Log configuration summary for debugging
	LogParameterSummary(fingerprint)
	
	// Step 4: Create verification tracer
	tracer := NewParameterVerificationTracer(fingerprint)
	
	// Step 5: Create gossipsub with all necessary options
	ps, err := pubsub.NewGossipSub(
		ctx,
		h,
		// Core gossipsub parameters
		pubsub.WithGossipSubParams(*gossipParams),
		// Peer scoring
		pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
		// Enable flood publishing for better message propagation
		pubsub.WithFloodPublish(true),
		// Add event tracer for monitoring
		pubsub.WithEventTracer(tracer),
		// Use versioned protocol ID for compatibility checking
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
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create gossipsub: %w", err)
	}
	
	// Step 6: Create monitoring service
	monitoringService := NewMonitoringService(h, ps, tracer, fingerprint)
	
	// Step 7: Start monitoring service on port 8080 (or configure as needed)
	go func() {
		if err := monitoringService.Start(8080); err != nil {
			log.Printf("Monitoring service failed: %v", err)
		}
	}()
	
	log.Printf("Gossipsub initialized with config version %s (hash: %s)", fingerprint.Version, fingerprint.Hash)
	log.Printf("Monitoring available at http://localhost:8080/gossipsub/status")
	
	return ps, tracer, monitoringService, nil
}

// IntegrateWithExistingPubSub adds monitoring to an existing pubsub instance
// This is useful if you've already created your pubsub but want to add monitoring
func IntegrateWithExistingPubSub(h host.Host, ps *pubsub.PubSub) (*MonitoringService, error) {
	// Get standard configuration for fingerprint generation
	gossipParams, peerScoreParams, peerScoreThresholds := ConfigureSnapshotSubmissionsMesh(h.ID())
	
	// Generate configuration fingerprint
	fingerprint, err := GenerateConfigFingerprint(gossipParams, peerScoreParams, peerScoreThresholds, true)
	if err != nil {
		return nil, fmt.Errorf("failed to generate config fingerprint: %w", err)
	}
	
	// Create a basic tracer (won't have full event tracking but can still verify peers)
	tracer := NewParameterVerificationTracer(fingerprint)
	
	// Create monitoring service
	monitoringService := NewMonitoringService(h, ps, tracer, fingerprint)
	
	// Start monitoring service
	go func() {
		if err := monitoringService.Start(8080); err != nil {
			log.Printf("Monitoring service failed: %v", err)
		}
	}()
	
	return monitoringService, nil
}

// VerifyPeerCompatibility can be called to manually verify a peer's configuration
func VerifyPeerCompatibility(tracer *ParameterVerificationTracer, peerID peer.ID, peerConfigJSON []byte) (bool, error) {
	var peerFingerprint ConfigFingerprint
	if err := json.Unmarshal(peerConfigJSON, &peerFingerprint); err != nil {
		return false, fmt.Errorf("failed to unmarshal peer config: %w", err)
	}
	
	compatible := tracer.VerifyPeerConfiguration(peerID, &peerFingerprint)
	return compatible, nil
}

// GetConfigurationJSON returns the local configuration as JSON for sharing
func GetConfigurationJSON(fingerprint *ConfigFingerprint) ([]byte, error) {
	return json.MarshalIndent(fingerprint, "", "  ")
}

// Monitoring Endpoints Available:
//
// GET /gossipsub/status - Full monitoring status including all peers and health metrics
// GET /gossipsub/health - Simple health check endpoint
// GET /gossipsub/parameters - Local gossipsub parameters and fingerprint
// GET /gossipsub/peers - Detailed peer configuration information
// POST /gossipsub/verify - Manually verify a specific peer's configuration
//
// Example curl commands:
//
// # Check overall status
// curl http://localhost:8080/gossipsub/status | jq .
//
// # Simple health check
// curl http://localhost:8080/gossipsub/health
//
// # View local parameters
// curl http://localhost:8080/gossipsub/parameters | jq .
//
// # Check peer configurations
// curl http://localhost:8080/gossipsub/peers | jq .
//
// # Manually verify a peer
// curl -X POST http://localhost:8080/gossipsub/verify \
//   -H "Content-Type: application/json" \
//   -d '{"peer_id":"12D3KooWExample..."}'