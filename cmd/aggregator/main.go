package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/ipfs"
	rediskeys "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/submissions"
	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

type Aggregator struct {
	ctx         context.Context
	cancel      context.CancelFunc
	redisClient *redis.Client
	ipfsClient  *ipfs.Client
	config      *config.Settings
	keyBuilder  *rediskeys.KeyBuilder

	// Track aggregation state
	epochBatches map[uint64]map[string]*consensus.FinalizedBatch // epochID -> validatorID -> batch
	mu           sync.RWMutex
}

func NewAggregator(cfg *config.Settings) (*Aggregator, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize Redis
	redisOpts := &redis.Options{
		Addr: fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		DB:   cfg.RedisDB,
	}
	// Only set password if it's not empty (trim spaces first)
	password := strings.TrimSpace(cfg.RedisPassword)
	if password != "" {
		redisOpts.Password = password
	}
	redisClient := redis.NewClient(redisOpts)

	if err := redisClient.Ping(ctx).Err(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Initialize IPFS client (optional)
	var ipfsClient *ipfs.Client
	if cfg.IPFSAPI != "" {
		client, err := ipfs.NewClient(cfg.IPFSAPI)
		if err != nil {
			log.WithError(err).Warn("Failed to connect to IPFS, continuing without it")
		} else {
			ipfsClient = client
		}
	}

	// Create key builder with first data market (assuming single market for now)
	protocolState := cfg.ProtocolStateContract
	dataMarket := ""
	if len(cfg.DataMarketAddresses) > 0 {
		dataMarket = cfg.DataMarketAddresses[0]
	}
	keyBuilder := rediskeys.NewKeyBuilder(protocolState, dataMarket)

	return &Aggregator{
		ctx:          ctx,
		cancel:       cancel,
		redisClient:  redisClient,
		ipfsClient:   ipfsClient,
		config:       cfg,
		keyBuilder:   keyBuilder,
		epochBatches: make(map[uint64]map[string]*consensus.FinalizedBatch),
	}, nil
}

func (a *Aggregator) processAggregationQueue() {
	// Get namespaced queue keys
	level1Queue := a.keyBuilder.AggregationQueueLevel1()
	level2Queue := a.keyBuilder.AggregationQueue()

	for {
		select {
		case <-a.ctx.Done():
			return
		default:
			// FIRST: Check for Level 1 aggregation (finalizer worker parts) - namespaced
			result, err := a.redisClient.BRPop(a.ctx, time.Second, level1Queue).Result()
			if err == nil && len(result) >= 2 {
				// Parse the complex JSON from finalizer workers
				var aggData map[string]interface{}
				if err := json.Unmarshal([]byte(result[1]), &aggData); err != nil {
					log.WithError(err).Error("Failed to parse aggregation data")
					continue
				}

				epochIDStr := aggData["epoch_id"].(string)
				partsCompleted := int(aggData["parts_completed"].(float64))

				log.WithFields(logrus.Fields{
					"epoch": epochIDStr,
					"parts": partsCompleted,
				}).Info("📦 LEVEL 1: Aggregating finalizer worker parts into local batch")

				// Aggregate worker parts into complete local batch
				a.aggregateWorkerParts(epochIDStr, partsCompleted)
				continue
			}

			// SECOND: Check for Level 2 aggregation (network-wide) - namespaced
			result, err = a.redisClient.BRPop(a.ctx, time.Second, level2Queue).Result()
			if err != nil {
				if err != redis.Nil {
					log.WithError(err).Debug("No epochs in aggregation queues")
				}
				continue
			}

			if len(result) < 2 {
				continue
			}

			epochID := result[1]
			log.WithField("epoch", epochID).Info("🌐 LEVEL 2: Aggregating network-wide validator batches")

			// Process Level 2 network aggregation
			a.aggregateEpoch(epochID)
		}
	}
}

func (a *Aggregator) aggregateWorkerParts(epochIDStr string, totalParts int) {
	// Convert string to uint64
	epochID, err := strconv.ParseUint(epochIDStr, 10, 64)
	if err != nil {
		log.WithError(err).Error("Failed to parse epoch ID")
		return
	}

	// Collect all batch parts from finalizer workers
	aggregatedResults := make(map[string]interface{})

	for i := 0; i < totalParts; i++ {
		partKey := a.keyBuilder.BatchPart(strconv.FormatUint(epochID, 10), i)
		partData, err := a.redisClient.Get(a.ctx, partKey).Result()
		if err != nil {
			log.Errorf("Failed to get batch part %d for epoch %d: %v", i, epochID, err)
			continue
		}

		var partResults map[string]interface{}
		if err := json.Unmarshal([]byte(partData), &partResults); err != nil {
			log.Errorf("Failed to parse batch part %d: %v", i, err)
			continue
		}

		// Merge results from this worker
		for projectID, data := range partResults {
			aggregatedResults[projectID] = data
		}

		// Clean up part data
		a.redisClient.Del(a.ctx, partKey)
	}

	// Create finalized batch from aggregated worker results
	finalizedBatch := a.createFinalizedBatchFromParts(epochID, aggregatedResults)

	// Store as our local finalized batch
	finalizedKey := a.keyBuilder.FinalizedBatch(strconv.FormatUint(epochID, 10))
	finalizedData, _ := json.Marshal(finalizedBatch)
	if err := a.redisClient.Set(a.ctx, finalizedKey, finalizedData, 24*time.Hour).Err(); err != nil {
		log.WithError(err).Error("Failed to store finalized batch")
		return
	}

	log.WithFields(logrus.Fields{
		"epoch":    epochID,
		"parts":    totalParts,
		"projects": len(aggregatedResults),
	}).Info("✅ LEVEL 1 COMPLETE: Created local finalized batch from worker parts")

	// CRITICAL: Broadcast our local batch to validator network
	broadcastMsg := map[string]interface{}{
		"type":    "finalized_batch",
		"epochId": epochID,
		"data":    finalizedBatch,
	}

	if msgData, err := json.Marshal(broadcastMsg); err == nil {
		// Use namespaced broadcast queue
		broadcastQueue := a.keyBuilder.OutgoingBroadcastBatch()
		if err := a.redisClient.LPush(a.ctx, broadcastQueue, msgData).Err(); err != nil {
			log.WithError(err).Error("Failed to queue batch for validator network broadcast")
		} else {
			log.WithFields(logrus.Fields{
				"epoch":    epochID,
				"projects": len(finalizedBatch.ProjectVotes),
				"cid":      finalizedBatch.BatchIPFSCID,
			}).Info("📡 Broadcasting LOCAL finalized batch to validator network")
		}
	}

	// Clean up tracking data (namespaced)
	a.redisClient.Del(a.ctx,
		a.keyBuilder.EpochPartsCompleted(epochIDStr),
		a.keyBuilder.EpochPartsTotal(epochIDStr),
		a.keyBuilder.EpochPartsReady(epochIDStr),
	)
}

func (a *Aggregator) createFinalizedBatchFromParts(epochID uint64, projectSubmissions map[string]interface{}) *consensus.FinalizedBatch {
	// Extract project data and create proper finalized batch
	projectIDs := make([]string, 0)
	snapshotCIDs := make([]string, 0)
	projectVotes := make(map[string]uint32)
	submissionDetails := make(map[string][]submissions.SubmissionMetadata)

	for projectID, submissionData := range projectSubmissions {
		if dataMap, ok := submissionData.(map[string]interface{}); ok {
			if cid, ok := dataMap["cid"].(string); ok {
				projectIDs = append(projectIDs, projectID)
				snapshotCIDs = append(snapshotCIDs, cid)

				// Extract vote count
				if votes, ok := dataMap["votes"].(float64); ok {
					projectVotes[projectID] = uint32(votes)
				} else {
					projectVotes[projectID] = 1
				}
			}
		}
	}

	// Create merkle root (simplified)
	combined := ""
	for i := range projectIDs {
		combined += projectIDs[i] + ":" + snapshotCIDs[i] + ","
	}
	hash := sha256.Sum256([]byte(combined))
	merkleRoot := hash[:]

	finalizedBatch := &consensus.FinalizedBatch{
		EpochId:           epochID,
		ProjectIds:        projectIDs,
		SnapshotCids:      snapshotCIDs,
		MerkleRoot:        merkleRoot,
		SequencerId:       a.config.SequencerID,
		Timestamp:         uint64(time.Now().Unix()),
		ProjectVotes:      projectVotes,
		SubmissionDetails: submissionDetails,
	}

	// Store in IPFS if available
	if a.ipfsClient != nil {
		if cid, err := a.ipfsClient.StoreFinalizedBatch(a.ctx, finalizedBatch); err == nil {
			finalizedBatch.BatchIPFSCID = cid
			log.WithFields(logrus.Fields{
				"epoch": epochID,
				"cid":   cid,
			}).Info("📦 Stored finalized batch in IPFS")
		}
	}

	return finalizedBatch
}

func (a *Aggregator) aggregateEpoch(epochIDStr string) {
	// Check if we've already aggregated this epoch recently (deduplication) - namespaced
	aggregatedKey := a.keyBuilder.BatchAggregated(epochIDStr)
	exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
	if err != nil {
		log.WithField("epoch", epochIDStr).WithError(err).Error("Failed to check aggregated status")
		return
	}
	if exists > 0 {
		log.WithField("epoch", epochIDStr).Debug("Epoch already aggregated, skipping")
		return
	}

	// Get our own finalized batch from the unified sequencer's finalizer
	// Use namespaced key
	ourBatchKey := a.keyBuilder.FinalizedBatch(epochIDStr)
	ourBatchData, _ := a.redisClient.Get(a.ctx, ourBatchKey).Result()

	if ourBatchData == "" {
		log.WithField("epoch", epochIDStr).Warn("No local finalized batch found for epoch")
		// Continue anyway - we might just aggregate other validators' batches
	}

	var ourBatch *consensus.FinalizedBatch
	if ourBatchData != "" {
		ourBatch = &consensus.FinalizedBatch{}
		if err := json.Unmarshal([]byte(ourBatchData), ourBatch); err != nil {
			log.WithError(err).Error("Failed to parse our batch")
			// Continue with other validators' batches
		}
	}

	// Get all incoming batches from OTHER validators for this epoch - namespaced
	incomingPattern := fmt.Sprintf("%s:%s:incoming:batch:%s:*", a.keyBuilder.ProtocolState, a.keyBuilder.DataMarket, epochIDStr)
	incomingKeys, err := a.redisClient.Keys(a.ctx, incomingPattern).Result()
	if err != nil {
		log.WithError(err).Error("Failed to get incoming batch keys")
		return
	}

	totalValidators := len(incomingKeys)
	if ourBatch != nil {
		totalValidators++ // Include ourselves
	}

	log.WithFields(logrus.Fields{
		"epoch": epochIDStr,
		"local_batch": ourBatch != nil,
		"incoming_batches": len(incomingKeys),
		"total_validators": totalValidators,
	}).Info("Starting epoch aggregation")

	// Aggregate all batches
	aggregatedBatch := a.createAggregatedBatch(ourBatch, incomingKeys)
	aggregatedData, _ := json.Marshal(aggregatedBatch)
	if err := a.redisClient.Set(a.ctx, aggregatedKey, aggregatedData, 24*time.Hour).Err(); err != nil {
		log.WithError(err).Error("Failed to store aggregated batch")
		return
	}

	log.WithFields(logrus.Fields{
		"epoch":            epochIDStr,
		"total_validators": totalValidators,
		"projects":         len(aggregatedBatch.ProjectVotes),
	}).Info("Aggregator: Completed aggregation")
}

func (a *Aggregator) createAggregatedBatch(ourBatch *consensus.FinalizedBatch, incomingKeys []string) consensus.FinalizedBatch {
	// Initialize aggregated batch
	aggregated := consensus.FinalizedBatch{
		SubmissionDetails: make(map[string][]submissions.SubmissionMetadata),
		ProjectVotes:      make(map[string]uint32),
		Timestamp:         uint64(time.Now().Unix()),
		SequencerId:       a.config.SequencerID, // Set our node's ID
	}

	// Track all validators' views
	validatorViews := make(map[string]*consensus.FinalizedBatch)

	// Add our batch if we have one
	if ourBatch != nil {
		aggregated.EpochId = ourBatch.EpochId
		validatorViews[ourBatch.SequencerId] = ourBatch

		// Add our submissions
		for projectID, submissions := range ourBatch.SubmissionDetails {
			aggregated.SubmissionDetails[projectID] = append(
				aggregated.SubmissionDetails[projectID],
				submissions...,
			)

			// Add our votes
			aggregated.ProjectVotes[projectID] = ourBatch.ProjectVotes[projectID]
		}
	}

	// Add incoming batches from other validators
	for _, key := range incomingKeys {
		batchData, err := a.redisClient.Get(a.ctx, key).Result()
		if err != nil {
			log.WithError(err).WithField("key", key).Error("Failed to get incoming batch")
			continue
		}

		var batch consensus.FinalizedBatch
		if err := json.Unmarshal([]byte(batchData), &batch); err != nil {
			log.WithError(err).Error("Failed to parse incoming batch")
			continue
		}

		// Track this validator's view
		validatorID := batch.SequencerId
		if validatorID == "" {
			// Extract from key as fallback
			parts := strings.Split(key, ":")
			if len(parts) >= 4 {
				validatorID = parts[3]
			}
		}

		if aggregated.EpochId == 0 {
			aggregated.EpochId = batch.EpochId
		}

		validatorViews[validatorID] = &batch

		// Merge submissions
		for projectID, submissions := range batch.SubmissionDetails {
			aggregated.SubmissionDetails[projectID] = append(
				aggregated.SubmissionDetails[projectID],
				submissions...,
			)

			// Merge votes (take max count)
			if currentCount, exists := aggregated.ProjectVotes[projectID]; !exists || batch.ProjectVotes[projectID] > currentCount {
				aggregated.ProjectVotes[projectID] = batch.ProjectVotes[projectID]
			}
		}
	}

	// Log aggregation summary
	log.WithFields(logrus.Fields{
		"epoch": aggregated.EpochId,
		"validators": len(validatorViews),
		"total_projects": len(aggregated.ProjectVotes),
	}).Info("📊 AGGREGATED FINALIZATION: Combined views from all validators")

	// Log which validators contributed
	for validatorID := range validatorViews {
		log.Debugf("  Validator %s contributed to aggregation", validatorID)
	}

	// Store aggregated batch to IPFS if available
	if a.ipfsClient != nil {
		if cid, err := a.ipfsClient.StoreFinalizedBatch(a.ctx, aggregated); err == nil {
			aggregated.BatchIPFSCID = cid
			log.WithFields(logrus.Fields{
				"epoch": aggregated.EpochId,
				"cid":   cid,
			}).Info("Aggregator: Stored aggregated batch to IPFS")
		}
	}

	// NOTE: Broadcasting is now handled by the unified sequencer after Level 1 aggregation
	// The aggregator component only performs Level 2 network-wide aggregation
	// No broadcasting needed here since the unified sequencer already queued it

	return aggregated
}

func (a *Aggregator) monitorFinalizedBatches() {
	// Watch for new finalized batches from our finalizer
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			// Check for new finalized batches - namespaced
			pattern := fmt.Sprintf("%s:%s:finalized:*", a.keyBuilder.ProtocolState, a.keyBuilder.DataMarket)
			keys, err := a.redisClient.Keys(a.ctx, pattern).Result()
			if err != nil {
				log.WithError(err).Debug("Failed to check finalized batches")
				continue
			}

			for _, key := range keys {
				// Extract epoch ID from namespaced key
				parts := strings.Split(key, ":")
				if len(parts) < 4 {
					continue
				}
				epochID := parts[len(parts)-1]

				// Check if we've already processed this epoch - namespaced
				aggregatedKey := a.keyBuilder.BatchAggregated(epochID)
				exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
				if err != nil || exists > 0 {
					continue
				}

				// Add to aggregation queue - namespaced
				aggQueue := a.keyBuilder.AggregationQueue()
				if err := a.redisClient.LPush(a.ctx, aggQueue, epochID).Err(); err != nil {
					log.WithError(err).Error("Failed to queue epoch for aggregation")
				} else {
					log.WithField("epoch", epochID).Info("Aggregator: Queued epoch for aggregation")
				}
			}
		}
	}
}

func (a *Aggregator) reportMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			// Count aggregated batches - namespaced
			pattern := fmt.Sprintf("%s:%s:batch:aggregated:*", a.keyBuilder.ProtocolState, a.keyBuilder.DataMarket)
			keys, _ := a.redisClient.Keys(a.ctx, pattern).Result()

			// Count active validators
			validatorPattern := "validator:active:*"
			validators, _ := a.redisClient.Keys(a.ctx, validatorPattern).Result()

			log.WithFields(logrus.Fields{
				"aggregated_batches": len(keys),
				"active_validators":  len(validators),
			}).Info("Aggregator metrics")
		}
	}
}

func (a *Aggregator) Start() error {
	log.Info("Starting Aggregator")

	// Start all processors
	go a.processAggregationQueue()
	go a.monitorFinalizedBatches()
	go a.reportMetrics()

	return nil
}

func (a *Aggregator) Stop() {
	log.Info("Stopping Aggregator")
	a.cancel()
	if a.redisClient != nil {
		a.redisClient.Close()
	}
}

func main() {
	// Setup logging
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	if os.Getenv("DEBUG_MODE") == "true" {
		log.SetLevel(logrus.DebugLevel)
	}

	// Load configuration
	if err := config.LoadConfig(); err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}
	cfg := config.SettingsObj

	// Create and start aggregator
	aggregator, err := NewAggregator(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create Aggregator")
	}

	if err := aggregator.Start(); err != nil {
		log.WithError(err).Fatal("Failed to start Aggregator")
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	aggregator.Stop()
}