package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/ipfs"
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

	return &Aggregator{
		ctx:          ctx,
		cancel:       cancel,
		redisClient:  redisClient,
		ipfsClient:   ipfsClient,
		config:       cfg,
		epochBatches: make(map[uint64]map[string]*consensus.FinalizedBatch),
	}, nil
}

func (a *Aggregator) processAggregationQueue() {
	for {
		select {
		case <-a.ctx.Done():
			return
		default:
			// Get next epoch to aggregate
			result, err := a.redisClient.BRPop(a.ctx, time.Second, "aggregation:queue").Result()
			if err != nil {
				if err != redis.Nil {
					log.WithError(err).Debug("No epochs in aggregation queue")
				}
				continue
			}

			if len(result) < 2 {
				continue
			}

			epochID := result[1]
			log.WithField("epoch", epochID).Info("Aggregator: Processing epoch")

			// Process this epoch
			a.aggregateEpoch(epochID)
		}
	}
}

func (a *Aggregator) aggregateEpoch(epochIDStr string) {
	// Check if we've already aggregated this epoch recently (deduplication)
	aggregatedKey := fmt.Sprintf("batch:aggregated:%s", epochIDStr)
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
	// Try both old and new key patterns
	ourBatchKey := ""
	ourBatchData := ""

	// Try new pattern: protocol:market:finalized:epochId
	pattern := fmt.Sprintf("*:*:finalized:%s", epochIDStr)
	keys, _ := a.redisClient.Keys(a.ctx, pattern).Result()
	if len(keys) > 0 {
		ourBatchKey = keys[0]
		ourBatchData, _ = a.redisClient.Get(a.ctx, ourBatchKey).Result()
	}

	// Fallback to old pattern if not found
	if ourBatchData == "" {
		ourBatchKey = fmt.Sprintf("batch:finalized:%s", epochIDStr)
		ourBatchData, _ = a.redisClient.Get(a.ctx, ourBatchKey).Result()
	}

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

	// Get all incoming batches from OTHER validators for this epoch
	incomingPattern := fmt.Sprintf("incoming:batch:%s:*", epochIDStr)
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

	// CRITICAL: Only broadcast if this is OUR local batch (not a network aggregation)
	// We broadcast when we have our own batch, not when just aggregating others
	if ourBatch != nil && len(validatorViews) == 1 {
		// This is our local finalization - broadcast it
		broadcastMsg := map[string]interface{}{
			"type":    "finalized_batch",
			"epochId": aggregated.EpochId,
			"data":    aggregated,
		}

		if msgData, err := json.Marshal(broadcastMsg); err == nil {
			if err := a.redisClient.LPush(a.ctx, "outgoing:broadcast:batch", msgData).Err(); err != nil {
				log.WithError(err).Error("Failed to queue batch for validator network broadcast")
			} else {
				log.WithFields(logrus.Fields{
					"epoch": aggregated.EpochId,
					"projects": len(aggregated.ProjectVotes),
					"cid": aggregated.BatchIPFSCID,
				}).Info("📡 Broadcasting LOCAL finalized batch to validator network")
			}
		}
	}

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
			// Check for new finalized batches
			pattern := "batch:finalized:*"
			keys, err := a.redisClient.Keys(a.ctx, pattern).Result()
			if err != nil {
				log.WithError(err).Debug("Failed to check finalized batches")
				continue
			}

			for _, key := range keys {
				// Extract epoch ID
				epochID := key[len("batch:finalized:"):]

				// Check if we've already processed this epoch
				aggregatedKey := fmt.Sprintf("batch:aggregated:%s", epochID)
				exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
				if err != nil || exists > 0 {
					continue
				}

				// Add to aggregation queue
				if err := a.redisClient.LPush(a.ctx, "aggregation:queue", epochID).Err(); err != nil {
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
			// Count aggregated batches
			pattern := "batch:aggregated:*"
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