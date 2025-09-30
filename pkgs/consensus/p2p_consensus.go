package consensus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/ipfs"
	log "github.com/sirupsen/logrus"
)

const (
	// Gossipsub topics for batch aggregation
	ValidatorPresenceTopic   = "/powerloom/validator/presence"     // For validator heartbeat/discovery
	FinalizedBatchesTopic    = "/powerloom/finalized-batches/all"  // For exchanging finalized batches

	// Consensus parameters
	MinValidatorsForConsensus = 2  // Minimum validators needed for consensus
	ConsensusThreshold        = 0.67 // 67% agreement required
	BatchValidityDuration     = 2 * time.Hour
)

// P2PConsensus handles validator consensus through P2P batch exchange
type P2PConsensus struct {
	ctx         context.Context
	cancel      context.CancelFunc
	host        host.Host
	pubsub      *pubsub.PubSub
	redisClient *redis.Client
	ipfsClient  *ipfs.Client
	sequencerID string

	// Topics and subscriptions
	discoveryTopic *pubsub.Topic
	batchTopic     *pubsub.Topic
	discoverySub   *pubsub.Subscription
	batchSub       *pubsub.Subscription

	// Consensus tracking
	validatorBatches map[uint64]map[string]*ValidatorBatch // epochID -> validatorID -> batch
	aggregationStatus map[uint64]*AggregationStatus        // epochID -> aggregation status
	mu               sync.RWMutex

	// Metrics
	receivedBatches  uint64
	broadcastBatches uint64
	epochsAggregated uint64
}

// ValidatorBatch represents a batch from a specific validator
type ValidatorBatch struct {
	ValidatorID   string                 `json:"validator_id"`
	PeerID        string                 `json:"peer_id"`
	EpochID       uint64                 `json:"epoch_id"`
	BatchIPFSCID  string                 `json:"batch_ipfs_cid"`
	MerkleRoot    string                 `json:"merkle_root"`
	ProjectCount  int                    `json:"project_count"`
	Timestamp     uint64                 `json:"timestamp"`
	Signature     string                 `json:"signature"`
	BatchData     *FinalizedBatch        `json:"-"` // Loaded from IPFS
	ReceivedAt    time.Time              `json:"received_at"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// AggregationStatus tracks the aggregated state of all validator batches for an epoch
type AggregationStatus struct {
	EpochID               uint64                       `json:"epoch_id"`
	TotalValidators       int                          `json:"total_validators"`
	ReceivedBatches       int                          `json:"received_batches"`
	ProjectVotes          map[string]map[string]int    `json:"project_votes"`      // projectID -> CID -> vote count
	AggregatedProjects    map[string]string            `json:"aggregated_projects"` // projectID -> winning CID
	ValidatorContributions map[string][]string         `json:"validator_contributions"` // validatorID -> list of projects they voted on
	UpdatedAt             time.Time                    `json:"updated_at"`
}

// NewP2PConsensus creates a new P2P consensus handler
func NewP2PConsensus(ctx context.Context, h host.Host, ps *pubsub.PubSub, redisClient *redis.Client, ipfsClient *ipfs.Client, sequencerID string) (*P2PConsensus, error) {
	consensusCtx, cancel := context.WithCancel(ctx)

	pc := &P2PConsensus{
		ctx:              consensusCtx,
		cancel:           cancel,
		host:             h,
		pubsub:           ps,
		redisClient:      redisClient,
		ipfsClient:       ipfsClient,
		sequencerID:      sequencerID,
		validatorBatches:  make(map[uint64]map[string]*ValidatorBatch),
		aggregationStatus: make(map[uint64]*AggregationStatus),
	}

	// Join topics
	if err := pc.setupTopics(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to setup topics: %w", err)
	}

	// Start message handlers
	go pc.handleDiscoveryMessages()
	go pc.handleBatchMessages()
	go pc.periodicConsensusCheck()
	go pc.sendPeriodicPresence()

	log.Infof("P2P Consensus initialized for validator %s", sequencerID)
	return pc, nil
}

// setupTopics initializes P2P topics and subscriptions
func (pc *P2PConsensus) setupTopics() error {
	// Join validator presence topic for discovery
	discoveryTopic, err := pc.pubsub.Join(ValidatorPresenceTopic)
	if err != nil {
		return fmt.Errorf("failed to join discovery topic: %w", err)
	}
	pc.discoveryTopic = discoveryTopic

	// Join batch topic
	batchTopic, err := pc.pubsub.Join(FinalizedBatchesTopic)
	if err != nil {
		return fmt.Errorf("failed to join batch topic: %w", err)
	}
	pc.batchTopic = batchTopic

	// Subscribe to both topics
	discoverySub, err := discoveryTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to discovery topic: %w", err)
	}
	pc.discoverySub = discoverySub

	batchSub, err := batchTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to batch topic: %w", err)
	}
	pc.batchSub = batchSub

	log.Infof("Joined aggregation topics: %s, %s", ValidatorPresenceTopic, FinalizedBatchesTopic)
	return nil
}

// BroadcastFinalizedBatch broadcasts a finalized batch to other validators
// The batch must already be stored in IPFS, and ipfsCID must be the CID where it was stored
func (pc *P2PConsensus) BroadcastFinalizedBatch(batch *FinalizedBatch, ipfsCID string) error {
	if ipfsCID == "" {
		return fmt.Errorf("cannot broadcast batch without IPFS CID")
	}

	// Create validator batch message
	merkleRootHex := hex.EncodeToString(batch.MerkleRoot)
	signatureHex := hex.EncodeToString(batch.BlsSignature)

	vBatch := &ValidatorBatch{
		ValidatorID:  pc.sequencerID,
		PeerID:       pc.host.ID().String(),
		EpochID:      batch.EpochId,
		BatchIPFSCID: ipfsCID,
		MerkleRoot:   merkleRootHex,
		ProjectCount: len(batch.ProjectIds),
		Timestamp:    batch.Timestamp,
		Signature:    signatureHex,
		Metadata: map[string]interface{}{
			"projects": len(batch.ProjectIds),
			"votes":    batch.ProjectVotes,
		},
	}

	// Marshal and broadcast
	data, err := json.Marshal(vBatch)
	if err != nil {
		return fmt.Errorf("failed to marshal batch: %w", err)
	}

	if err := pc.batchTopic.Publish(pc.ctx, data); err != nil {
		return fmt.Errorf("failed to publish batch: %w", err)
	}

	pc.broadcastBatches++
	log.Infof("📡 Broadcasted finalized batch for epoch %d (CID: %s, Merkle: %s...)",
		batch.EpochId, ipfsCID, merkleRootHex[:16])

	// Also store our own batch for consensus checking
	pc.storeValidatorBatch(vBatch)

	return nil
}

// handleBatchMessages processes incoming batch messages from other validators
func (pc *P2PConsensus) handleBatchMessages() {
	for {
		select {
		case <-pc.ctx.Done():
			return
		default:
			msg, err := pc.batchSub.Next(pc.ctx)
			if err != nil {
				if pc.ctx.Err() != nil {
					return
				}
				log.Errorf("Error reading batch message: %v", err)
				continue
			}

			// Skip own messages
			if msg.ReceivedFrom == pc.host.ID() {
				continue
			}

			// Process batch message
			go pc.processBatchMessage(msg)
		}
	}
}

// processBatchMessage handles a single batch message from another validator
func (pc *P2PConsensus) processBatchMessage(msg *pubsub.Message) {
	var vBatch ValidatorBatch
	if err := json.Unmarshal(msg.Data, &vBatch); err != nil {
		log.Errorf("Failed to unmarshal batch message: %v", err)
		return
	}

	vBatch.ReceivedAt = time.Now()

	log.Infof("📨 Received batch from validator %s for epoch %d (CID: %s, Merkle: %s...)",
		vBatch.ValidatorID, vBatch.EpochID, vBatch.BatchIPFSCID, vBatch.MerkleRoot[:16])

	// Validate batch
	if err := pc.validateBatch(&vBatch); err != nil {
		log.Errorf("Invalid batch from %s: %v", vBatch.ValidatorID, err)
		return
	}

	// Store batch
	pc.storeValidatorBatch(&vBatch)

	// Store in Redis for persistence
	if err := pc.storeBatchInRedis(&vBatch); err != nil {
		log.Errorf("Failed to store batch in Redis: %v", err)
	}

	pc.receivedBatches++

	// Aggregate votes for this epoch
	pc.aggregateEpochVotes(vBatch.EpochID)
}

// validateBatch performs validation on received batch
func (pc *P2PConsensus) validateBatch(vBatch *ValidatorBatch) error {
	// Basic validation
	if vBatch.BatchIPFSCID == "" {
		return fmt.Errorf("missing IPFS CID")
	}

	if vBatch.MerkleRoot == "" {
		return fmt.Errorf("missing merkle root")
	}

	if vBatch.ValidatorID == "" {
		return fmt.Errorf("missing validator ID")
	}

	if vBatch.EpochID == 0 {
		return fmt.Errorf("invalid epoch ID")
	}

	// TODO: Validate signature when BLS is implemented
	// For now, just check signature exists
	if vBatch.Signature == "" {
		log.Warnf("Batch from %s has no signature (mocked for now)", vBatch.ValidatorID)
	}

	// Optionally fetch and validate IPFS content
	if pc.ipfsClient != nil {
		// This is async validation - we'll fetch the actual batch data later if needed
		go pc.fetchBatchFromIPFS(vBatch)
	}

	return nil
}

// fetchBatchFromIPFS retrieves the full batch data from IPFS
func (pc *P2PConsensus) fetchBatchFromIPFS(vBatch *ValidatorBatch) {
	if vBatch.BatchIPFSCID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch from IPFS
	data, err := pc.ipfsClient.RetrieveFinalizedBatch(ctx, vBatch.BatchIPFSCID)
	if err != nil {
		log.Errorf("Failed to fetch batch %s from IPFS: %v", vBatch.BatchIPFSCID, err)
		return
	}

	// Parse the batch
	var batch FinalizedBatch
	if err := json.Unmarshal(data, &batch); err != nil {
		log.Errorf("Failed to parse batch from IPFS: %v", err)
		return
	}

	// Verify merkle root matches
	calculatedRoot := pc.calculateMerkleRoot(batch.ProjectIds, batch.SnapshotCids)
	if hex.EncodeToString(calculatedRoot) != vBatch.MerkleRoot {
		log.Errorf("Merkle root mismatch for batch from %s", vBatch.ValidatorID)
		return
	}

	// Store the full batch data
	pc.mu.Lock()
	if epochBatches, exists := pc.validatorBatches[vBatch.EpochID]; exists {
		if vb, ok := epochBatches[vBatch.ValidatorID]; ok {
			vb.BatchData = &batch
		}
	}
	pc.mu.Unlock()

	log.Debugf("Successfully fetched and validated full batch data from IPFS for validator %s", vBatch.ValidatorID)
}

// storeValidatorBatch stores a batch in memory
func (pc *P2PConsensus) storeValidatorBatch(vBatch *ValidatorBatch) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if _, exists := pc.validatorBatches[vBatch.EpochID]; !exists {
		pc.validatorBatches[vBatch.EpochID] = make(map[string]*ValidatorBatch)
	}

	pc.validatorBatches[vBatch.EpochID][vBatch.ValidatorID] = vBatch
}

// storeBatchInRedis persists validator batch to Redis
func (pc *P2PConsensus) storeBatchInRedis(vBatch *ValidatorBatch) error {
	key := fmt.Sprintf("validator:%s:batch:%d", vBatch.ValidatorID, vBatch.EpochID)

	data, err := json.Marshal(vBatch)
	if err != nil {
		return fmt.Errorf("failed to marshal batch: %w", err)
	}

	// Store with TTL
	if err := pc.redisClient.Set(pc.ctx, key, data, BatchValidityDuration).Err(); err != nil {
		return fmt.Errorf("failed to store in Redis: %w", err)
	}

	// Also add to epoch validator set
	epochKey := fmt.Sprintf("epoch:%d:validators", vBatch.EpochID)
	pc.redisClient.SAdd(pc.ctx, epochKey, vBatch.ValidatorID)
	pc.redisClient.Expire(pc.ctx, epochKey, BatchValidityDuration)

	return nil
}

// aggregateEpochVotes aggregates project votes from all validators for an epoch
func (pc *P2PConsensus) aggregateEpochVotes(epochID uint64) {
	pc.mu.RLock()
	epochBatches := pc.validatorBatches[epochID]
	pc.mu.RUnlock()

	if len(epochBatches) < MinValidatorsForConsensus {
		log.Debugf("Epoch %d: Only %d/%d validators reported", epochID, len(epochBatches), MinValidatorsForConsensus)
		return
	}

	// Aggregate project votes from all validators
	// projectID -> CID -> vote count
	projectVotes := make(map[string]map[string]int)
	validatorContributions := make(map[string][]string)
	totalValidators := len(epochBatches)

	for validatorID, vBatch := range epochBatches {
		// Extract project votes from metadata
		if votes, ok := vBatch.Metadata["votes"].(map[string]interface{}); ok {
			for projectID, cidData := range votes {
				if projectVotes[projectID] == nil {
					projectVotes[projectID] = make(map[string]int)
				}

				// Count this validator's vote for this project->CID mapping
				if cidMap, ok := cidData.(map[string]interface{}); ok {
					if cid, ok := cidMap["cid"].(string); ok {
						projectVotes[projectID][cid]++
						validatorContributions[validatorID] = append(validatorContributions[validatorID], projectID)
					}
				} else if cid, ok := cidData.(string); ok {
					// Direct CID string
					projectVotes[projectID][cid]++
					validatorContributions[validatorID] = append(validatorContributions[validatorID], projectID)
				}
			}
		}

		// If BatchData is loaded from IPFS, use ProjectIds and SnapshotCids
		if vBatch.BatchData != nil && len(vBatch.BatchData.ProjectIds) > 0 {
			for i, projectID := range vBatch.BatchData.ProjectIds {
				if i < len(vBatch.BatchData.SnapshotCids) {
					cid := vBatch.BatchData.SnapshotCids[i]

					if projectVotes[projectID] == nil {
						projectVotes[projectID] = make(map[string]int)
					}
					projectVotes[projectID][cid]++
					validatorContributions[validatorID] = append(validatorContributions[validatorID], projectID)
				}
			}
		}
	}

	// Determine winning CID for each project (highest vote count)
	aggregatedProjects := make(map[string]string)
	for projectID, cidVotes := range projectVotes {
		var winningCID string
		maxVotes := 0

		for cid, votes := range cidVotes {
			if votes > maxVotes {
				maxVotes = votes
				winningCID = cid
			}
		}

		// Only include if it has sufficient votes (could be configurable threshold)
		if float64(maxVotes)/float64(totalValidators) >= 0.5 { // 50% threshold for now
			aggregatedProjects[projectID] = winningCID
		}
	}

	// Update aggregation status
	status := &AggregationStatus{
		EpochID:               epochID,
		TotalValidators:       totalValidators,
		ReceivedBatches:       len(epochBatches),
		ProjectVotes:          projectVotes,
		AggregatedProjects:    aggregatedProjects,
		ValidatorContributions: validatorContributions,
		UpdatedAt:             time.Now(),
	}

	pc.mu.Lock()
	pc.aggregationStatus[epochID] = status
	pc.mu.Unlock()

	// Store aggregation status in Redis
	pc.storeAggregationStatus(status)

	pc.epochsAggregated++

	log.Infof("📊 AGGREGATION for epoch %d: %d validators contributed, %d projects aggregated",
		epochID, len(epochBatches), len(aggregatedProjects))

	// Log project details
	for projectID, winningCID := range aggregatedProjects {
		if votes := projectVotes[projectID][winningCID]; votes > 0 {
			log.Debugf("  Project %s: CID %s (%d/%d votes)",
				projectID, winningCID[:12]+"...", votes, len(epochBatches))
		}
	}

	// Trigger aggregation complete actions (e.g., proposer election and submission)
	pc.onAggregationComplete(epochID, aggregatedProjects)
}

// storeAggregationStatus saves aggregation status to Redis
func (pc *P2PConsensus) storeAggregationStatus(status *AggregationStatus) error {
	key := fmt.Sprintf("consensus:epoch:%d:status", status.EpochID)

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	if err := pc.redisClient.Set(pc.ctx, key, data, BatchValidityDuration).Err(); err != nil {
		return fmt.Errorf("failed to store status: %w", err)
	}

	return nil
}

// onAggregationComplete handles actions when aggregation is complete
func (pc *P2PConsensus) onAggregationComplete(epochID uint64, aggregatedProjects map[string]string) {
	// Calculate merkle root from aggregated projects
	projectIDs := make([]string, 0, len(aggregatedProjects))
	snapshotCIDs := make([]string, 0, len(aggregatedProjects))

	for projectID, cid := range aggregatedProjects {
		projectIDs = append(projectIDs, projectID)
		snapshotCIDs = append(snapshotCIDs, cid)
	}

	merkleRoot := pc.calculateMerkleRoot(projectIDs, snapshotCIDs)
	merkleRootHex := fmt.Sprintf("%x", merkleRoot)

	// Create a combined CID representation (for now, use merkle root as placeholder)
	// In production, this would be the IPFS CID of the aggregated batch
	consensusCID := merkleRootHex[:16] + "..."

	// Store consensus result
	key := fmt.Sprintf("consensus:epoch:%d:result", epochID)
	result := map[string]interface{}{
		"epoch_id":     epochID,
		"cid":          consensusCID,
		"merkle_root":  merkleRootHex,
		"validator_id": pc.sequencerID,
		"timestamp":    time.Now().Unix(),
		"projects":     len(aggregatedProjects),
	}

	data, _ := json.Marshal(result)
	pc.redisClient.Set(pc.ctx, key, data, BatchValidityDuration)

	// TODO: Trigger on-chain submission when contracts are ready
	log.Infof("🎯 Ready to submit consensus batch for epoch %d to chain (CID: %s, %d projects)",
		epochID, consensusCID, len(aggregatedProjects))
}

// periodicConsensusCheck periodically checks consensus status for recent epochs
func (pc *P2PConsensus) periodicConsensusCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pc.ctx.Done():
			return
		case <-ticker.C:
			pc.checkRecentEpochs()
			pc.logAggregationStats()
		}
	}
}

// checkRecentEpochs checks consensus for recent epochs
func (pc *P2PConsensus) checkRecentEpochs() {
	currentEpoch := uint64(time.Now().Unix() / 30) // Assuming 30-second epochs

	// Check last 5 epochs
	for i := uint64(0); i < 5; i++ {
		epochID := currentEpoch - i
		pc.aggregateEpochVotes(epochID)
	}
}

// logAggregationStats logs aggregation statistics
func (pc *P2PConsensus) logAggregationStats() {
	pc.mu.RLock()
	activeEpochs := len(pc.validatorBatches)
	aggregatedCount := 0
	totalProjects := 0
	for _, status := range pc.aggregationStatus {
		if len(status.AggregatedProjects) > 0 {
			aggregatedCount++
			totalProjects += len(status.AggregatedProjects)
		}
	}
	pc.mu.RUnlock()

	peers := pc.getConnectedValidators()

	log.Infof("====== BATCH AGGREGATION STATUS ======")
	log.Infof("Connected Validators: %d", len(peers))
	log.Infof("Active Epochs: %d", activeEpochs)
	log.Infof("Epochs Aggregated: %d", aggregatedCount)
	log.Infof("Total Projects: %d", totalProjects)
	log.Infof("Batches Received: %d", pc.receivedBatches)
	log.Infof("Batches Broadcast: %d", pc.broadcastBatches)
	log.Infof("==============================")
}

// handleDiscoveryMessages processes discovery/presence messages
func (pc *P2PConsensus) handleDiscoveryMessages() {
	for {
		select {
		case <-pc.ctx.Done():
			return
		default:
			msg, err := pc.discoverySub.Next(pc.ctx)
			if err != nil {
				if pc.ctx.Err() != nil {
					return
				}
				continue
			}

			// Skip own messages
			if msg.ReceivedFrom == pc.host.ID() {
				continue
			}

			// Log presence for network monitoring
			log.Debugf("Validator presence from peer %s", msg.ReceivedFrom.ShortString())
		}
	}
}

// sendPeriodicPresence sends periodic presence messages for discovery
func (pc *P2PConsensus) sendPeriodicPresence() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Send initial presence
	pc.sendPresenceMessage()

	for {
		select {
		case <-pc.ctx.Done():
			return
		case <-ticker.C:
			pc.sendPresenceMessage()
		}
	}
}

// sendPresenceMessage broadcasts validator presence for discovery
func (pc *P2PConsensus) sendPresenceMessage() error {
	presence := map[string]interface{}{
		"type":         "validator_presence",
		"validator_id": pc.sequencerID,
		"peer_id":      pc.host.ID().String(),
		"timestamp":    time.Now().Unix(),
		"capabilities": []string{"consensus", "ipfs", "batch_validation"},
	}

	data, err := json.Marshal(presence)
	if err != nil {
		return err
	}

	if err := pc.discoveryTopic.Publish(pc.ctx, data); err != nil {
		return err
	}

	log.Debugf("Sent validator presence message")
	return nil
}

// getConnectedValidators returns list of connected validator peers
func (pc *P2PConsensus) getConnectedValidators() []peer.ID {
	peers := make(map[peer.ID]bool)

	// Get peers from discovery topic
	for _, p := range pc.discoveryTopic.ListPeers() {
		peers[p] = true
	}

	// Get peers from batch topic
	for _, p := range pc.batchTopic.ListPeers() {
		peers[p] = true
	}

	// Convert to list
	peerList := make([]peer.ID, 0, len(peers))
	for p := range peers {
		if p != pc.host.ID() {
			peerList = append(peerList, p)
		}
	}

	return peerList
}

// GetAggregationStatus returns the aggregation status for an epoch
func (pc *P2PConsensus) GetAggregationStatus(epochID uint64) *AggregationStatus {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.aggregationStatus[epochID]
}

// GetValidatorBatches returns all validator batches for an epoch
func (pc *P2PConsensus) GetValidatorBatches(epochID uint64) map[string]*ValidatorBatch {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.validatorBatches[epochID]
}

// calculateMerkleRoot computes merkle root from project data
func (pc *P2PConsensus) calculateMerkleRoot(projectIDs, snapshotCIDs []string) []byte {
	combined := ""
	for i := range projectIDs {
		combined += projectIDs[i] + ":" + snapshotCIDs[i] + ","
	}
	hash := sha256.Sum256([]byte(combined))
	return hash[:]
}

// Close cleanly shuts down the consensus handler
func (pc *P2PConsensus) Close() error {
	pc.cancel()

	if pc.discoveryTopic != nil {
		pc.discoveryTopic.Close()
	}
	if pc.batchTopic != nil {
		pc.batchTopic.Close()
	}
	if pc.discoverySub != nil {
		pc.discoverySub.Cancel()
	}
	if pc.batchSub != nil {
		pc.batchSub.Cancel()
	}

	return nil
}