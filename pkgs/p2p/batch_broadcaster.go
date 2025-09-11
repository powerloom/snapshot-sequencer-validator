package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const (
	// Two-level topics for finalized batch broadcasting (no dynamic epoch topics)
	FinalizedBatchDiscoveryTopic = "/powerloom/finalized-batches/0"   // Discovery/rendezvous
	FinalizedBatchAllTopic       = "/powerloom/finalized-batches/all" // Actual voting messages
)

// BatchBroadcaster handles P2P communication for finalized batches
type BatchBroadcaster struct {
	host       host.Host
	pubsub     *pubsub.PubSub
	ctx        context.Context
	sequencerID string
	
	// Topics and subscriptions (two-level only, no dynamic epoch topics)
	discoveryTopic *pubsub.Topic
	allTopic       *pubsub.Topic
	discoverySub   *pubsub.Subscription
	allSub         *pubsub.Subscription
	
	// Channels for received batches
	IncomingBatches chan *FinalizedBatchMessage
}

// FinalizedBatchMessage represents a batch received from P2P network
type FinalizedBatchMessage struct {
	Batch    interface{} // Will be *FinalizedBatch after protobuf generation
	PeerID   string
	Received time.Time
}

// NewBatchBroadcaster creates a new P2P broadcaster for finalized batches
func NewBatchBroadcaster(ctx context.Context, h host.Host, ps *pubsub.PubSub, sequencerID string) (*BatchBroadcaster, error) {
	bb := &BatchBroadcaster{
		host:            h,
		pubsub:          ps,
		ctx:             ctx,
		sequencerID:     sequencerID,
		IncomingBatches: make(chan *FinalizedBatchMessage, 100),
	}
	
	// Join discovery topic for peer discovery
	discoveryTopic, err := ps.Join(FinalizedBatchDiscoveryTopic)
	if err != nil {
		return nil, fmt.Errorf("failed to join discovery topic: %w", err)
	}
	bb.discoveryTopic = discoveryTopic
	
	// Join the "all" topic for actual voting messages
	allTopic, err := ps.Join(FinalizedBatchAllTopic)
	if err != nil {
		return nil, fmt.Errorf("failed to join all topic: %w", err)
	}
	bb.allTopic = allTopic
	
	// Subscribe to both topics
	discoverySub, err := discoveryTopic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to discovery topic: %w", err)
	}
	bb.discoverySub = discoverySub
	
	allSub, err := allTopic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to all topic: %w", err)
	}
	bb.allSub = allSub
	
	// Start listening for batches
	go bb.handleIncomingBatches()
	
	log.Printf("BatchBroadcaster initialized for sequencer %s", sequencerID)
	return bb, nil
}

// SendPresenceMessage sends a presence message to the discovery topic for peer discovery
func (bb *BatchBroadcaster) SendPresenceMessage() error {
	presence := map[string]interface{}{
		"type":        "validator_presence",
		"sequencer_id": bb.sequencerID,
		"peer_id":     bb.host.ID().String(),
		"timestamp":   time.Now().Unix(),
	}
	
	data, err := json.Marshal(presence)
	if err != nil {
		return fmt.Errorf("failed to marshal presence: %w", err)
	}
	
	// Send to discovery topic only
	if err := bb.discoveryTopic.Publish(bb.ctx, data); err != nil {
		return fmt.Errorf("failed to publish presence: %w", err)
	}
	
	log.Printf("Sent presence message to discovery topic")
	return nil
}

// BroadcastBatch broadcasts a finalized batch to the network
// Per Phase 2-3 spec: Exchange IPFS CID, not full batch data
func (bb *BatchBroadcaster) BroadcastBatch(batch interface{}, epochID uint64) error {
	// Extract IPFS CID from batch if available
	var batchIPFSCID string
	if batchMap, ok := batch.(map[string]interface{}); ok {
		if cid, ok := batchMap["batch_ipfs_cid"].(string); ok {
			batchIPFSCID = cid
		}
	}
	
	// If no IPFS CID, we can't broadcast per Phase 2-3 requirements
	if batchIPFSCID == "" {
		return fmt.Errorf("cannot broadcast batch without IPFS CID (Phase 2-3 requirement)")
	}
	
	// Create vote message with IPFS CID only
	voteMessage := map[string]interface{}{
		"type":                "finalized_batch_vote",
		"batch_ipfs_cid":      batchIPFSCID,  // Send CID, not full batch
		"epoch_id":            epochID,
		"sequencer_id":        bb.sequencerID,
		"peer_id":             bb.host.ID().String(),
		"broadcast_timestamp": time.Now().Unix(),
	}
	
	// Marshal vote message
	data, err := json.Marshal(voteMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal vote message: %w", err)
	}
	
	// Broadcast ONLY to "all" topic (no dynamic epoch topics)
	if err := bb.allTopic.Publish(bb.ctx, data); err != nil {
		return fmt.Errorf("failed to publish to all topic: %w", err)
	}
	
	log.Printf("Broadcasted finalized batch vote for epoch %d (IPFS CID: %s)", epochID, batchIPFSCID)
	return nil
}

// handleIncomingBatches processes incoming batches from subscriptions
func (bb *BatchBroadcaster) handleIncomingBatches() {
	// Handle discovery topic in separate goroutine
	go func() {
		for {
			select {
			case <-bb.ctx.Done():
				return
			default:
				msg, err := bb.discoverySub.Next(bb.ctx)
				if err == nil && msg.ReceivedFrom != bb.host.ID() {
					// Just log presence messages, don't process as batches
					log.Printf("Discovery message from %s", msg.ReceivedFrom)
				}
			}
		}
	}()
	
	// Handle voting messages from all topic
	for {
		select {
		case <-bb.ctx.Done():
			return
		default:
			msg, err := bb.allSub.Next(bb.ctx)
			if err == nil {
				bb.processMessage(msg)
			}
		}
	}
}

// processMessage processes a received pubsub message
func (bb *BatchBroadcaster) processMessage(msg *pubsub.Message) {
	// Skip own messages
	if msg.ReceivedFrom == bb.host.ID() {
		return
	}
	
	// Parse message
	var data map[string]interface{}
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		log.Printf("Failed to unmarshal message: %v", err)
		return
	}
	
	// Check message type (vote messages with IPFS CIDs)
	msgType, _ := data["type"].(string)
	if msgType != "finalized_batch_vote" {
		return // Ignore non-vote messages
	}
	
	// Extract IPFS CID and metadata
	batchIPFSCID, _ := data["batch_ipfs_cid"].(string)
	sequencerID, _ := data["sequencer_id"].(string)
	peerID, _ := data["peer_id"].(string)
	epochID, _ := data["epoch_id"].(float64) // JSON numbers are float64
	
	// Create vote message with IPFS CID reference
	voteMessage := map[string]interface{}{
		"batch_ipfs_cid": batchIPFSCID,
		"sequencer_id":   sequencerID,
		"epoch_id":       epochID,
	}
	
	// Send to channel for processing
	bb.IncomingBatches <- &FinalizedBatchMessage{
		Batch:    voteMessage,  // Now contains CID, not full batch
		PeerID:   peerID,
		Received: time.Now(),
	}
	
	log.Printf("Received finalized batch vote from sequencer %s for epoch %.0f (IPFS CID: %s)", 
		sequencerID, epochID, batchIPFSCID)
}

// GetConnectedPeers returns the list of connected peers on the finalized batch topics
func (bb *BatchBroadcaster) GetConnectedPeers() []peer.ID {
	peers := make(map[peer.ID]bool)
	
	// Get peers from discovery topic
	for _, p := range bb.discoveryTopic.ListPeers() {
		peers[p] = true
	}
	
	// Get peers from all topic
	for _, p := range bb.allTopic.ListPeers() {
		peers[p] = true
	}
	
	// Convert to list
	peerList := make([]peer.ID, 0, len(peers))
	for p := range peers {
		if p != bb.host.ID() { // Exclude self
			peerList = append(peerList, p)
		}
	}
	
	return peerList
}

// Close cleanly shuts down the broadcaster
func (bb *BatchBroadcaster) Close() error {
	if bb.discoveryTopic != nil {
		bb.discoveryTopic.Close()
	}
	if bb.allTopic != nil {
		bb.allTopic.Close()
	}
	if bb.discoverySub != nil {
		bb.discoverySub.Cancel()
	}
	if bb.allSub != nil {
		bb.allSub.Cancel()
	}
	close(bb.IncomingBatches)
	return nil
}