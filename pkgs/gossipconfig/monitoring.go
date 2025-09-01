// Package gossipconfig provides HTTP monitoring endpoints for gossipsub parameter verification
package gossipconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// MonitoringService provides HTTP endpoints for gossipsub parameter monitoring
type MonitoringService struct {
	mu              sync.RWMutex
	host            host.Host
	pubsub          *pubsub.PubSub
	tracer          *ParameterVerificationTracer
	fingerprint     *ConfigFingerprint
	startTime       time.Time
	messageExchange *MessageExchange
}

// MessageExchange handles parameter exchange between peers
type MessageExchange struct {
	mu                sync.RWMutex
	localFingerprint  *ConfigFingerprint
	peerFingerprints  map[peer.ID]*PeerInfo
	lastExchangeTime  map[peer.ID]time.Time
}

// PeerInfo contains information about a peer's configuration
type PeerInfo struct {
	PeerID      peer.ID           `json:"peer_id"`
	Fingerprint *ConfigFingerprint `json:"fingerprint"`
	LastSeen    time.Time         `json:"last_seen"`
	Compatible  bool              `json:"compatible"`
	Issues      []string          `json:"issues,omitempty"`
}

// MonitoringStatus represents the overall monitoring status
type MonitoringStatus struct {
	LocalFingerprint    *ConfigFingerprint       `json:"local_fingerprint"`
	Uptime              string                   `json:"uptime"`
	TotalPeers          int                      `json:"total_peers"`
	ConnectedPeers      []string                 `json:"connected_peers"`
	MeshHealth          map[string]*MeshHealth   `json:"mesh_health"`
	IncompatiblePeers   map[string]string        `json:"incompatible_peers"`
	ParameterMismatches []ParameterMismatch      `json:"parameter_mismatches,omitempty"`
	PeerConfigurations  map[string]*PeerInfo     `json:"peer_configurations"`
}

// NewMonitoringService creates a new monitoring service
func NewMonitoringService(h host.Host, ps *pubsub.PubSub, tracer *ParameterVerificationTracer, fingerprint *ConfigFingerprint) *MonitoringService {
	return &MonitoringService{
		host:        h,
		pubsub:      ps,
		tracer:      tracer,
		fingerprint: fingerprint,
		startTime:   time.Now(),
		messageExchange: &MessageExchange{
			localFingerprint:  fingerprint,
			peerFingerprints:  make(map[peer.ID]*PeerInfo),
			lastExchangeTime:  make(map[peer.ID]time.Time),
		},
	}
}

// Start begins the monitoring service on the specified port
func (m *MonitoringService) Start(port int) error {
	// Set up HTTP routes
	http.HandleFunc("/gossipsub/status", m.handleStatus)
	http.HandleFunc("/gossipsub/health", m.handleHealth)
	http.HandleFunc("/gossipsub/parameters", m.handleParameters)
	http.HandleFunc("/gossipsub/peers", m.handlePeers)
	http.HandleFunc("/gossipsub/verify", m.handleVerify)
	
	// Start parameter exchange protocol
	m.startParameterExchange()
	
	// Start HTTP server
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting gossipsub monitoring service on %s\n", addr)
	return http.ListenAndServe(addr, nil)
}

// handleStatus returns the overall monitoring status
func (m *MonitoringService) handleStatus(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// Get connected peers
	connectedPeers := m.host.Network().Peers()
	peerStrings := make([]string, len(connectedPeers))
	for i, p := range connectedPeers {
		peerStrings[i] = p.String()
	}
	
	// Get mesh health from tracer
	meshHealth := m.tracer.GetMeshHealth()
	
	// Get incompatible peers
	incompatiblePeers := m.tracer.GetIncompatiblePeers()
	incompatibleStrings := make(map[string]string)
	for pid, reason := range incompatiblePeers {
		incompatibleStrings[pid.String()] = reason
	}
	
	// Get parameter mismatches
	mismatches := m.tracer.GetParameterMismatches()
	
	// Get peer configurations
	peerConfigs := m.getPeerConfigurations()
	
	status := MonitoringStatus{
		LocalFingerprint:    m.fingerprint,
		Uptime:              time.Since(m.startTime).String(),
		TotalPeers:          len(connectedPeers),
		ConnectedPeers:      peerStrings,
		MeshHealth:          meshHealth,
		IncompatiblePeers:   incompatibleStrings,
		ParameterMismatches: mismatches,
		PeerConfigurations:  peerConfigs,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleHealth returns simplified health check
func (m *MonitoringService) handleHealth(w http.ResponseWriter, r *http.Request) {
	meshHealth := m.tracer.GetMeshHealth()
	incompatibleCount := len(m.tracer.GetIncompatiblePeers())
	
	health := map[string]interface{}{
		"status": "healthy",
		"uptime": time.Since(m.startTime).String(),
		"config_version": m.fingerprint.Version,
		"config_hash": m.fingerprint.Hash,
		"total_peers": len(m.host.Network().Peers()),
		"incompatible_peers": incompatibleCount,
		"topics": len(meshHealth),
	}
	
	// Check if we have mesh peers on important topics
	hasSnapshotPeers := false
	for topic, health := range meshHealth {
		if topic == "/powerloom/snapshot-submissions/all" && health.MeshPeers > 0 {
			hasSnapshotPeers = true
			break
		}
	}
	
	if !hasSnapshotPeers && len(m.host.Network().Peers()) > 0 {
		health["status"] = "degraded"
		health["message"] = "No mesh peers on snapshot submission topic"
	}
	
	if incompatibleCount > 0 {
		health["status"] = "warning"
		health["message"] = fmt.Sprintf("%d peers have incompatible configurations", incompatibleCount)
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// handleParameters returns the local gossipsub parameters
func (m *MonitoringService) handleParameters(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"version":     m.fingerprint.Version,
		"hash":        m.fingerprint.Hash,
		"timestamp":   m.fingerprint.Timestamp,
		"parameters":  m.fingerprint.Parameters,
		"protocol_id": ProtocolIDWithVersion(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handlePeers returns detailed peer configuration information
func (m *MonitoringService) handlePeers(w http.ResponseWriter, r *http.Request) {
	peerConfigs := m.getPeerConfigurations()
	
	response := map[string]interface{}{
		"total_peers":      len(m.host.Network().Peers()),
		"verified_peers":   len(peerConfigs),
		"configurations":   peerConfigs,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleVerify allows manual verification of a peer's configuration
func (m *MonitoringService) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		PeerID string `json:"peer_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	
	peerID, err := peer.Decode(req.PeerID)
	if err != nil {
		http.Error(w, "Invalid peer ID", http.StatusBadRequest)
		return
	}
	
	// Request parameter exchange with specific peer
	if err := m.requestParameterExchange(peerID); err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}
	
	// Wait a moment for exchange to complete
	time.Sleep(2 * time.Second)
	
	// Get peer configuration
	peerInfo := m.getPeerInfo(peerID)
	
	response := map[string]interface{}{
		"success":   true,
		"peer_info": peerInfo,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// startParameterExchange sets up the protocol for exchanging parameters with peers
func (m *MonitoringService) startParameterExchange() {
	protocolID := protocol.ID("/powerloom/gossipsub/params/1.0.0")
	
	m.host.SetStreamHandler(protocolID, func(s network.Stream) {
		defer s.Close()
		
		// Receive peer's fingerprint
		var peerFingerprint ConfigFingerprint
		if err := json.NewDecoder(s).Decode(&peerFingerprint); err != nil {
			fmt.Printf("Failed to decode peer fingerprint: %v\n", err)
			return
		}
		
		// Send our fingerprint
		if err := json.NewEncoder(s).Encode(m.fingerprint); err != nil {
			fmt.Printf("Failed to send fingerprint: %v\n", err)
			return
		}
		
		// Verify configuration
		peerID := s.Conn().RemotePeer()
		compatible := m.tracer.VerifyPeerConfiguration(peerID, &peerFingerprint)
		
		// Store peer info
		m.messageExchange.mu.Lock()
		m.messageExchange.peerFingerprints[peerID] = &PeerInfo{
			PeerID:      peerID,
			Fingerprint: &peerFingerprint,
			LastSeen:    time.Now(),
			Compatible:  compatible,
		}
		m.messageExchange.lastExchangeTime[peerID] = time.Now()
		m.messageExchange.mu.Unlock()
		
		fmt.Printf("Parameter exchange with %s: compatible=%v, version=%s, hash=%s\n", 
			peerID.String()[:8], compatible, peerFingerprint.Version, peerFingerprint.Hash)
	})
	
	// Periodically exchange parameters with connected peers
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		for range ticker.C {
			peers := m.host.Network().Peers()
			for _, peerID := range peers {
				// Check if we've exchanged recently
				m.messageExchange.mu.RLock()
				lastExchange, exists := m.messageExchange.lastExchangeTime[peerID]
				m.messageExchange.mu.RUnlock()
				
				if !exists || time.Since(lastExchange) > 5*time.Minute {
					go m.requestParameterExchange(peerID)
				}
			}
		}
	}()
}

// requestParameterExchange initiates parameter exchange with a peer
func (m *MonitoringService) requestParameterExchange(peerID peer.ID) error {
	protocolID := protocol.ID("/powerloom/gossipsub/params/1.0.0")
	
	s, err := m.host.NewStream(network.WithNoDial(context.Background(), "already connected"), peerID, protocolID)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer s.Close()
	
	// Send our fingerprint
	if err := json.NewEncoder(s).Encode(m.fingerprint); err != nil {
		return fmt.Errorf("failed to send fingerprint: %w", err)
	}
	
	// Receive peer's fingerprint
	var peerFingerprint ConfigFingerprint
	if err := json.NewDecoder(s).Decode(&peerFingerprint); err != nil {
		return fmt.Errorf("failed to decode peer fingerprint: %w", err)
	}
	
	// Verify configuration
	compatible := m.tracer.VerifyPeerConfiguration(peerID, &peerFingerprint)
	
	// Store peer info
	m.messageExchange.mu.Lock()
	m.messageExchange.peerFingerprints[peerID] = &PeerInfo{
		PeerID:      peerID,
		Fingerprint: &peerFingerprint,
		LastSeen:    time.Now(),
		Compatible:  compatible,
	}
	m.messageExchange.lastExchangeTime[peerID] = time.Now()
	m.messageExchange.mu.Unlock()
	
	return nil
}

// getPeerConfigurations returns all known peer configurations
func (m *MonitoringService) getPeerConfigurations() map[string]*PeerInfo {
	m.messageExchange.mu.RLock()
	defer m.messageExchange.mu.RUnlock()
	
	result := make(map[string]*PeerInfo)
	for pid, info := range m.messageExchange.peerFingerprints {
		result[pid.String()] = info
	}
	return result
}

// getPeerInfo returns configuration info for a specific peer
func (m *MonitoringService) getPeerInfo(peerID peer.ID) *PeerInfo {
	m.messageExchange.mu.RLock()
	defer m.messageExchange.mu.RUnlock()
	
	if info, ok := m.messageExchange.peerFingerprints[peerID]; ok {
		return info
	}
	return nil
}

// LogParameterSummary logs a summary of the gossipsub parameters for debugging
func LogParameterSummary(fingerprint *ConfigFingerprint) {
	fmt.Println("========================================")
	fmt.Println("Gossipsub Configuration Summary")
	fmt.Println("========================================")
	fmt.Printf("Version: %s\n", fingerprint.Version)
	fmt.Printf("Hash: %s\n", fingerprint.Hash)
	fmt.Printf("Protocol ID: %s\n", ProtocolIDWithVersion())
	fmt.Println("----------------------------------------")
	fmt.Println("Mesh Parameters:")
	fmt.Printf("  D=%d, Dlo=%d, Dhi=%d\n", 
		fingerprint.Parameters.D, 
		fingerprint.Parameters.Dlo, 
		fingerprint.Parameters.Dhi)
	fmt.Printf("  Dlazy=%d, Dout=%d, Dscore=%d\n",
		fingerprint.Parameters.Dlazy,
		fingerprint.Parameters.Dout,
		fingerprint.Parameters.Dscore)
	fmt.Println("Timing:")
	fmt.Printf("  Heartbeat: %dms\n", fingerprint.Parameters.HeartbeatIntervalMs)
	fmt.Printf("  Prune Backoff: %dms\n", fingerprint.Parameters.PruneBackoffMs)
	fmt.Println("History:")
	fmt.Printf("  Length=%d, Gossip=%d\n",
		fingerprint.Parameters.HistoryLength,
		fingerprint.Parameters.HistoryGossip)
	fmt.Println("Features:")
	fmt.Printf("  Flood Publish: %v\n", fingerprint.Parameters.FloodPublish)
	fmt.Println("Scoring Weights:")
	fmt.Printf("  Topic Weight: %.1f\n", fingerprint.Parameters.TopicWeight)
	fmt.Printf("  Time in Mesh: %.1f\n", fingerprint.Parameters.TimeInMeshWeight)
	fmt.Printf("  First Deliveries: %.1f\n", fingerprint.Parameters.FirstMessageDeliveriesWeight)
	fmt.Printf("  Mesh Deliveries: %.1f (disabled if 0)\n", fingerprint.Parameters.MeshMessageDeliveriesWeight)
	fmt.Printf("  Mesh Failures: %.1f (disabled if 0)\n", fingerprint.Parameters.MeshFailurePenaltyWeight)
	fmt.Println("Thresholds:")
	fmt.Printf("  Gossip: %.0f\n", fingerprint.Parameters.GossipThreshold)
	fmt.Printf("  Publish: %.0f\n", fingerprint.Parameters.PublishThreshold)
	fmt.Printf("  Graylist: %.0f\n", fingerprint.Parameters.GraylistThreshold)
	fmt.Println("========================================")
}