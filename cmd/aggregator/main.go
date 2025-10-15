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
	epochTimers  map[uint64]*time.Timer                          // epochID -> aggregation window timer
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

	aggregator := &Aggregator{
		ctx:          ctx,
		cancel:       cancel,
		redisClient:  redisClient,
		ipfsClient:   ipfsClient,
		config:       cfg,
		keyBuilder:   keyBuilder,
		epochBatches: make(map[uint64]map[string]*consensus.FinalizedBatch),
		epochTimers:  make(map[uint64]*time.Timer),
	}

	// Initialize stream consumer if enabled
	if cfg.EnableStreamNotifications {
		if err := aggregator.initializeStreamConsumer(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to initialize stream consumer: %w", err)
		}
	}

	return aggregator, nil
}

// initializeStreamConsumer sets up the aggregator as a stream consumer
func (a *Aggregator) initializeStreamConsumer() error {
	streamKey := a.keyBuilder.AggregationStream()
	groupName := a.config.StreamConsumerGroup
	consumerName := a.config.StreamConsumerName

	log.WithFields(logrus.Fields{
		"stream":     streamKey,
		"group":      groupName,
		"consumer":   consumerName,
	}).Info("Initializing Redis stream consumer")

	// Ensure consumer group exists (create with stream if needed)
	err := a.redisClient.XGroupCreateMkStream(a.ctx, streamKey, groupName, "0").Err()
	if err != nil {
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			return fmt.Errorf("failed to create consumer group: %w", err)
		}
		log.WithField("group", groupName).Info("Consumer group already exists")
	}

	// Start stream consumer goroutine
	go a.consumeStreamMessages()

	// Start consumer health monitoring
	go a.monitorConsumerHealth()

	return nil
}

// consumeStreamMessages consumes messages from the aggregation stream
func (a *Aggregator) consumeStreamMessages() {
	streamKey := a.keyBuilder.AggregationStream()
	groupName := a.config.StreamConsumerGroup
	consumerName := a.config.StreamConsumerName

	log.WithFields(logrus.Fields{
		"stream":   streamKey,
		"group":    groupName,
		"consumer": consumerName,
	}).Info("Starting stream consumer")

	for {
		select {
		case <-a.ctx.Done():
			return
		default:
			// Read messages from stream
			messages, err := a.redisClient.XReadGroup(a.ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: consumerName,
				Streams:  []string{streamKey, ">"},
				Count:    int64(a.config.StreamBatchSize),
				Block:    a.config.StreamReadBlock,
			}).Result()

			if err != nil {
				if err == redis.Nil {
					// No messages available, continue
					continue
				}
				if a.ctx.Err() != nil {
					// Context cancelled, exit
					return
				}
				log.WithError(err).Error("Failed to read from stream")
				time.Sleep(5 * time.Second) // Back off on error
				continue
			}

			// Process received messages
			for _, stream := range messages {
				for _, message := range stream.Messages {
					if err := a.processStreamMessage(message); err != nil {
						log.WithError(err).WithFields(logrus.Fields{
							"message_id": message.ID,
							"stream":     streamKey,
						}).Error("Failed to process stream message")

						// Move problematic message to dead letter queue
						a.moveToDeadLetterQueue(streamKey, groupName, message)
					}
				}
			}
		}
	}
}

// processStreamMessage processes a single stream message
func (a *Aggregator) processStreamMessage(message redis.XMessage) error {
	// Extract message fields
	epoch, ok := message.Values["epoch"].(string)
	if !ok {
		return fmt.Errorf("missing epoch field in message")
	}

	validator, ok := message.Values["validator"].(string)
	if !ok {
		return fmt.Errorf("missing validator field in message")
	}

	_, ok = message.Values["batch_key"].(string)
	if !ok {
		return fmt.Errorf("missing batch_key field in message")
	}

	timestamp, ok := message.Values["timestamp"].(string)
	if !ok {
		return fmt.Errorf("missing timestamp field in message")
	}

	msgType, ok := message.Values["type"].(string)
	if !ok {
		return fmt.Errorf("missing type field in message")
	}

	log.WithFields(logrus.Fields{
		"message_id": message.ID,
		"epoch":      epoch,
		"validator":  validator,
		"type":       msgType,
		"timestamp":  timestamp,
	}).Debug("Processing stream message")

	// Only process validator batch messages
	if msgType != "validator_batch" {
		log.WithField("type", msgType).Debug("Ignoring non-validator-batch message")
		return nil
	}

	// Check if epoch is already aggregated
	aggregatedKey := a.keyBuilder.BatchAggregated(epoch)
	exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
	if err != nil {
		return fmt.Errorf("failed to check aggregation status: %w", err)
	}

	if exists > 0 {
		log.WithField("epoch", epoch).Debug("Epoch already aggregated, skipping message")
		return nil
	}

	// Start or extend aggregation window for this epoch
	a.startAggregationWindow(epoch)

	return nil
}

// moveToDeadLetterQueue moves problematic messages to a dead letter queue
func (a *Aggregator) moveToDeadLetterQueue(streamKey, groupName string, message redis.XMessage) {
	deadLetterKey := streamKey + ":dlq"

	// Add message to dead letter queue with metadata
	dlqData := map[string]interface{}{
		"original_id":    message.ID,
		"values":         message.Values,
		"error_time":     time.Now().Unix(),
		"consumer_group": groupName,
		"error_reason":   "processing_failed",
	}

	if err := a.redisClient.XAdd(a.ctx, &redis.XAddArgs{
		Stream: deadLetterKey,
		Values: dlqData,
	}).Err(); err != nil {
		log.WithError(err).Error("Failed to add message to dead letter queue")
	}

	// Acknowledge the original message to remove it from the pending list
	a.redisClient.XAck(a.ctx, streamKey, groupName, message.ID)

	log.WithFields(logrus.Fields{
		"message_id":  message.ID,
		"dead_letter": deadLetterKey,
	}).Warn("Moved problematic message to dead letter queue")
}

// monitorConsumerHealth monitors the health of the stream consumer
func (a *Aggregator) monitorConsumerHealth() {
	ticker := time.NewTicker(120 * time.Second) // Check every 2 minutes
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			streamKey := a.keyBuilder.AggregationStream()
			groupName := a.config.StreamConsumerGroup
			consumerName := a.config.StreamConsumerName

			// Check consumer info
			consumers, err := a.redisClient.XInfoConsumers(a.ctx, streamKey, groupName).Result()
			if err != nil {
				log.WithError(err).Error("Failed to get consumer info")
				continue
			}

			// Find our consumer
			var ourConsumer *redis.XInfoConsumer
			for _, consumer := range consumers {
				if consumer.Name == consumerName {
					ourConsumer = &consumer
					break
				}
			}

			if ourConsumer == nil {
				log.WithField("consumer", consumerName).Warn("Our consumer not found in group")
				continue
			}

			// Log consumer health
			log.WithFields(logrus.Fields{
				"consumer": consumerName,
				"pending":  ourConsumer.Pending,
				"idle":     ourConsumer.Idle,
			}).Debug("Consumer health check")

			// Check for long idle time (potential consumer stall)
			if time.Duration(ourConsumer.Idle) > a.config.StreamIdleTimeout {
				log.WithFields(logrus.Fields{
					"consumer": consumerName,
					"idle":     ourConsumer.Idle,
					"pending":  ourConsumer.Pending,
				}).Warn("Consumer appears stalled")

				// Attempt to claim stalled messages
				a.claimStalledMessages(streamKey, groupName, consumerName)
			}
		}
	}
}

// claimStalledMessages claims messages that have been pending too long
func (a *Aggregator) claimStalledMessages(streamKey, groupName, consumerName string) {
	// Get pending messages for our consumer
	pending, err := a.redisClient.XPendingExt(a.ctx, &redis.XPendingExtArgs{
		Stream:   streamKey,
		Group:    groupName,
		Start:    "-",
		End:      "+",
		Count:    10, // Process in batches
		Consumer: consumerName,
	}).Result()

	if err != nil {
		log.WithError(err).Error("Failed to get pending messages")
		return
	}

	// Claim messages that have been idle longer than the timeout
	minIdleTime := a.config.StreamIdleTimeout
	claimedCount := 0

	for _, pendingMsg := range pending {
		if pendingMsg.Idle >= minIdleTime {
			// Claim the message for ourselves
			messages, err := a.redisClient.XClaim(a.ctx, &redis.XClaimArgs{
				Stream:   streamKey,
				Group:    groupName,
				Consumer: consumerName,
				MinIdle:  minIdleTime,
				Messages: []string{pendingMsg.ID},
			}).Result()

			if err != nil {
				log.WithError(err).WithField("message_id", pendingMsg.ID).Error("Failed to claim message")
				continue
			}

			if len(messages) > 0 {
				claimedCount++
				log.WithField("message_id", messages[0].ID).Info("Claimed stalled message")

				// Process the claimed message
				if err := a.processStreamMessage(messages[0]); err != nil {
					log.WithError(err).WithField("message_id", messages[0].ID).Error("Failed to process claimed message")
				}
			}
		}
	}

	if claimedCount > 0 {
		log.WithField("claimed_count", claimedCount).Info("Processed stalled messages")
	}
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

			// SECOND: Check for Level 2 aggregation (network-wide) - ONLY if stream notifications disabled
			if !a.config.EnableStreamNotifications {
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
				log.WithField("epoch", epochID).Info("🌐 LEVEL 2: Starting aggregation window for network-wide validator batches (queue-based)")

				// Start or extend aggregation window for this epoch
				a.startAggregationWindow(epochID)
			} else {
				// Stream notifications enabled - aggregation is triggered by stream messages
				// Just sleep briefly to prevent busy-waiting
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

// startAggregationWindow initiates or extends the aggregation window for Level 2
func (a *Aggregator) startAggregationWindow(epochIDStr string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Convert to uint64 for map key
	epochID, err := strconv.ParseUint(epochIDStr, 10, 64)
	if err != nil {
		log.WithError(err).Error("Failed to parse epoch ID for aggregation window")
		return
	}

	// Check if timer already exists
	if _, exists := a.epochTimers[epochID]; exists {
		// Window already started - just log that we received another batch
		log.WithField("epoch", epochID).Info("⏱️  Additional validator batch received during aggregation window")
		return
	}

	// Start new aggregation window timer
	timer := time.AfterFunc(a.config.AggregationWindowDuration, func() {
		log.WithFields(logrus.Fields{
			"epoch":   epochID,
			"window":  a.config.AggregationWindowDuration,
		}).Info("⏰ Aggregation window expired - finalizing Level 2 aggregation")

		// Perform aggregation after window expires
		a.aggregateEpoch(epochIDStr)

		// Clean up timer
		a.mu.Lock()
		delete(a.epochTimers, epochID)
		a.mu.Unlock()
	})

	a.epochTimers[epochID] = timer
	log.WithFields(logrus.Fields{
		"epoch":   epochID,
		"window":  a.config.AggregationWindowDuration,
	}).Info("⏱️  Started Level 2 aggregation window - collecting validator batches")
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

	// Add monitoring metrics for Level 1 aggregation
	timestamp := time.Now().Unix()

	// Pipeline for monitoring metrics
	pipe := a.redisClient.Pipeline()

	// 1. Add to batches timeline
	pipe.ZAdd(a.ctx, a.keyBuilder.MetricsBatchesTimeline(), &redis.Z{
		Score:  float64(timestamp),
		Member: fmt.Sprintf("local:%d", epochID),
	})

	// 2. Store local batch metrics with TTL
	batchMetricsKey := a.keyBuilder.MetricsBatchLocal(strconv.FormatUint(epochID, 10))
	batchMetricsData := map[string]interface{}{
		"epoch_id":      epochID,
		"type":          "local",
		"validator_id":  a.config.SequencerID,
		"ipfs_cid":      finalizedBatch.BatchIPFSCID,
		"merkle_root":   finalizedBatch.MerkleRoot,
		"project_count": len(finalizedBatch.ProjectIds),
		"parts_merged":  totalParts,
		"timestamp":     timestamp,
	}
	jsonData, _ := json.Marshal(batchMetricsData)
	pipe.SetEX(a.ctx, batchMetricsKey, string(jsonData), 24*time.Hour)

	// 3. Add to validator batches timeline
	validatorBatchesKey := a.keyBuilder.MetricsValidatorBatches(a.config.SequencerID)
	pipe.ZAdd(a.ctx, validatorBatchesKey, &redis.Z{
		Score:  float64(timestamp),
		Member: epochID,
	})

	// 4. Publish state change
	pipe.Publish(a.ctx, "state:change", fmt.Sprintf("batch:local:%d", epochID))

	// Execute pipeline (ignore errors - monitoring is non-critical)
	if _, err := pipe.Exec(a.ctx); err != nil {
		log.Debugf("Failed to write monitoring metrics: %v", err)
	}

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

				// Extract submission metadata (WHO submitted WHAT for rewards)
				if metadataRaw, exists := dataMap["submission_metadata"]; exists {
					if metadataArray, ok := metadataRaw.([]interface{}); ok {
						// Convert to proper SubmissionMetadata structs
						projectMetadata := make([]submissions.SubmissionMetadata, 0)
						for _, metaItem := range metadataArray {
							if metaMap, ok := metaItem.(map[string]interface{}); ok {
								metadata := submissions.SubmissionMetadata{}
								if submitterID, ok := metaMap["submitter_id"].(string); ok {
									metadata.SubmitterID = submitterID
								}
								if snapshotCID, ok := metaMap["snapshot_cid"].(string); ok {
									metadata.SnapshotCID = snapshotCID
								}
								if timestamp, ok := metaMap["timestamp"].(float64); ok {
									metadata.Timestamp = uint64(timestamp)
								}
								if slotID, ok := metaMap["slot_id"].(float64); ok {
									metadata.SlotID = uint64(slotID)
								}
								if signature, ok := metaMap["signature"].(string); ok {
									metadata.Signature = []byte(signature)
								}
								// Initialize validators_confirming with this validator's ID
								metadata.ValidatorsConfirming = []string{a.config.SequencerID}
								metadata.VoteCount = 1 // Each submission counts as 1 vote
								projectMetadata = append(projectMetadata, metadata)
							}
						}
						submissionDetails[projectID] = projectMetadata
					}
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
		log.WithField("epoch", epochIDStr).Info("✅ Epoch already aggregated, skipping re-aggregation")
		return
	}

	// Get our own finalized batch from the unified sequencer's finalizer
	// Use namespaced key
	ourBatchKey := a.keyBuilder.FinalizedBatch(epochIDStr)
	var ourBatchData string
	ourBatchData, _ = a.redisClient.Get(a.ctx, ourBatchKey).Result()

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

	// Get all validators for this epoch using deterministic approach
	epochValidatorsKey := a.keyBuilder.EpochValidators(epochIDStr)
	validatorIDs, err := a.redisClient.SMembers(a.ctx, epochValidatorsKey).Result()
	if err != nil {
		log.WithError(err).WithField("epoch", epochIDStr).Error("Failed to get epoch validators")
		// Continue with local batch only if validator set is not available
		validatorIDs = []string{}
	}

	// Construct incoming batch keys deterministically
	incomingKeys := make([]string, 0)
	for _, validatorID := range validatorIDs {
		// Skip our own validator ID - we already have our local batch
		if validatorID == a.config.SequencerID {
			continue
		}
		batchKey := a.keyBuilder.IncomingBatch(epochIDStr, validatorID)
		incomingKeys = append(incomingKeys, batchKey)
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

	// Add monitoring metrics for Level 2 aggregation
	timestamp := time.Now().Unix()
	epochID, _ := strconv.ParseUint(epochIDStr, 10, 64)

	// Pipeline for monitoring metrics
	pipe := a.redisClient.Pipeline()

	// 1. Add to batches timeline
	pipe.ZAdd(a.ctx, a.keyBuilder.MetricsBatchesTimeline(), &redis.Z{
		Score:  float64(timestamp),
		Member: fmt.Sprintf("aggregated:%s", epochIDStr),
	})

	// 2. Store aggregated batch metrics with TTL
	batchMetricsKey := a.keyBuilder.MetricsBatchAggregated(epochIDStr)
	batchMetricsData := map[string]interface{}{
		"epoch_id":         epochID,
		"type":             "aggregated",
		"validators_count": totalValidators,
		"project_count":    len(aggregatedBatch.ProjectVotes),
		"timestamp":        timestamp,
		"validator_ids":    extractValidatorIDs(incomingKeys),
		"ipfs_cid":         aggregatedBatch.BatchIPFSCID,
		"merkle_root":      aggregatedBatch.MerkleRoot,
	}
	jsonData, _ := json.Marshal(batchMetricsData)
	pipe.SetEX(a.ctx, batchMetricsKey, string(jsonData), 24*time.Hour)

	// 3. Store validator list with TTL (include local + remote validators)
	validatorsKey := a.keyBuilder.MetricsBatchValidators(epochIDStr)
	allValidators := extractValidatorIDs(incomingKeys)
	// Add local validator ID
	allValidators = append(allValidators, a.config.SequencerID)
	validatorList, _ := json.Marshal(allValidators)
	pipe.SetEX(a.ctx, validatorsKey, string(validatorList), 24*time.Hour)

	// 4. Publish state change
	pipe.Publish(a.ctx, "state:change", fmt.Sprintf("batch:aggregated:%s", epochIDStr))

	// Execute pipeline (ignore errors - monitoring is non-critical)
	if _, err := pipe.Exec(a.ctx); err != nil {
		log.Debugf("Failed to write monitoring metrics: %v", err)
	}

	// CRITICAL: Clean up source finalized batch to prevent re-aggregation loop
	keysToDelete := []string{ourBatchKey}
	keysToDelete = append(keysToDelete, incomingKeys...)

	if deleted, err := a.redisClient.Del(a.ctx, keysToDelete...).Result(); err != nil {
		log.WithError(err).Warn("Failed to clean up source batches after aggregation")
	} else {
		log.WithFields(logrus.Fields{
			"epoch": epochIDStr,
			"keys_deleted": deleted,
		}).Info("🗑️  Cleaned up source batches after successful aggregation")
	}
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

	// Store validator IPFS CIDs
	validatorBatchCIDs := make(map[string]string)

	// Add incoming batches from other validators
	for _, key := range incomingKeys {
		batchData, err := a.redisClient.Get(a.ctx, key).Result()
		if err != nil {
			log.WithError(err).WithField("key", key).Error("Failed to get incoming batch")
			continue
		}

		var batch consensus.FinalizedBatch
		var validatorID string

		// First try to unmarshal as ValidatorBatch (P2P message format)
		var vBatch consensus.ValidatorBatch
		if err := json.Unmarshal([]byte(batchData), &vBatch); err == nil && vBatch.BatchIPFSCID != "" {
			// Store the CID mapping
			validatorBatchCIDs[vBatch.ValidatorID] = vBatch.BatchIPFSCID
			validatorID = vBatch.ValidatorID

			// Try to extract FinalizedBatch data (might be embedded or need IPFS fetch)
			if err := json.Unmarshal([]byte(batchData), &batch); err != nil {
				log.WithField("validator", validatorID).Debug("ValidatorBatch format detected but missing FinalizedBatch data")
				continue
			}
		} else {
			// Fallback: try as FinalizedBatch directly
			if err := json.Unmarshal([]byte(batchData), &batch); err != nil {
				log.WithError(err).Error("Failed to parse incoming batch")
				continue
			}

			// Track this validator's view
			validatorID = batch.SequencerId
			if validatorID == "" {
				// Extract from key as fallback
				parts := strings.Split(key, ":")
				if len(parts) >= 4 {
					validatorID = parts[3]
				}
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

	// Merge duplicate submissions: combine validators_confirming for same submitter+CID
	for projectID, subs := range aggregated.SubmissionDetails {
		merged := make(map[string]*submissions.SubmissionMetadata) // key: submitter_id:snapshot_cid

		for i := range subs {
			sub := &subs[i]
			key := sub.SubmitterID + ":" + sub.SnapshotCID

			if existing, found := merged[key]; found {
				// Same submission seen by multiple validators - merge validator lists
				existing.ValidatorsConfirming = append(existing.ValidatorsConfirming, sub.ValidatorsConfirming...)
				existing.VoteCount++
			} else {
				merged[key] = sub
			}
		}

		// Replace with merged submissions
		mergedList := make([]submissions.SubmissionMetadata, 0, len(merged))
		for _, sub := range merged {
			mergedList = append(mergedList, *sub)
		}
		aggregated.SubmissionDetails[projectID] = mergedList
	}

	// Build ProjectIds and SnapshotCids arrays from aggregated data
	// Determine consensus CID for each project (most votes)
	projectIDs := make([]string, 0, len(aggregated.ProjectVotes))
	snapshotCIDs := make([]string, 0, len(aggregated.ProjectVotes))

	for projectID := range aggregated.ProjectVotes {
		projectIDs = append(projectIDs, projectID)

		// Find consensus CID (most submitted)
		cidCounts := make(map[string]int)
		if submissions, exists := aggregated.SubmissionDetails[projectID]; exists {
			for _, sub := range submissions {
				cidCounts[sub.SnapshotCID]++
			}
		}

		// Get CID with highest count
		var consensusCID string
		maxCount := 0
		for cid, count := range cidCounts {
			if count > maxCount {
				maxCount = count
				consensusCID = cid
			}
		}
		snapshotCIDs = append(snapshotCIDs, consensusCID)
	}

	aggregated.ProjectIds = projectIDs
	aggregated.SnapshotCids = snapshotCIDs

	// Calculate merkle root from consensus data
	combined := ""
	for i := range projectIDs {
		combined += projectIDs[i] + ":" + snapshotCIDs[i] + ","
	}
	hash := sha256.Sum256([]byte(combined))
	aggregated.MerkleRoot = hash[:]

	// Store validator batch IPFS CIDs for attribution tracking
	aggregated.ValidatorBatches = validatorBatchCIDs

	// Log aggregation summary
	log.WithFields(logrus.Fields{
		"epoch": aggregated.EpochId,
		"validators": len(validatorViews),
		"total_projects": len(aggregated.ProjectVotes),
		"consensus_cids": len(snapshotCIDs),
	}).Info("📊 AGGREGATED FINALIZATION: Combined views from all validators")

	// Log which validators contributed
	for validatorID := range validatorViews {
		log.Debugf("  Validator %s contributed to aggregation", validatorID)
	}

	// Store aggregated batch to IPFS if available
	if a.ipfsClient != nil {
		if cid, err := a.ipfsClient.StoreFinalizedBatch(a.ctx, &aggregated); err == nil {
			aggregated.BatchIPFSCID = cid // Store CID in batch for monitoring
			log.WithFields(logrus.Fields{
				"epoch": aggregated.EpochId,
				"cid":   cid,
			}).Info("Aggregator: Stored aggregated batch to IPFS")
		}
	}

	// Set validator count
	aggregated.ValidatorCount = len(validatorViews)

	// NOTE: Broadcasting is now handled by the unified sequencer after Level 1 aggregation
	// The aggregator component only performs Level 2 network-wide aggregation
	// No broadcasting needed here since the unified sequencer already queued it

	return aggregated
}

func (a *Aggregator) monitorFinalizedBatches() {
	// Watch for new finalized batches from our finalizer
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	queuedEpochs := make(map[string]bool)
	lastCleanup := time.Now()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			// Periodic cleanup of queued tracking (every 5 minutes)
			if time.Since(lastCleanup) > 5*time.Minute {
				queuedEpochs = make(map[string]bool)
				lastCleanup = time.Now()
				log.Debug("Reset queued epochs tracking")
			}

			// Check for new finalized batches using active epochs tracking
			// Get active epochs from the ActiveEpochs set
			activeEpochsKey := a.keyBuilder.ActiveEpochs()
			activeEpochs, err := a.redisClient.SMembers(a.ctx, activeEpochsKey).Result()
			if err != nil {
				log.WithError(err).Debug("Failed to get active epochs")
				continue
			}

			// Construct finalized batch keys for active epochs
			keys := make([]string, 0, len(activeEpochs))
			for _, epochID := range activeEpochs {
				finalizedKey := a.keyBuilder.FinalizedBatch(epochID)
				// Check if the finalized batch exists
				exists, err := a.redisClient.Exists(a.ctx, finalizedKey).Result()
				if err == nil && exists > 0 {
					keys = append(keys, finalizedKey)
				}
			}

			newBatchesFound := 0
			// Process each active epoch directly
			for _, epochID := range activeEpochs {
				// Skip if already queued this session
				if queuedEpochs[epochID] {
					continue
				}

				// Check if we've already processed this epoch - namespaced
				aggregatedKey := a.keyBuilder.BatchAggregated(epochID)
				exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
				if err != nil {
					log.WithError(err).Warn("Failed to check aggregated status")
					continue
				}
				if exists > 0 {
					// Already aggregated, mark as queued to avoid re-checking
					queuedEpochs[epochID] = true
					continue
				}

				// Check if finalized batch exists for this epoch
				finalizedKey := a.keyBuilder.FinalizedBatch(epochID)
				exists, err = a.redisClient.Exists(a.ctx, finalizedKey).Result()
				if err != nil || exists == 0 {
					continue // No finalized batch yet
				}

				// Add to aggregation queue - namespaced
				aggQueue := a.keyBuilder.AggregationQueue()
				if err := a.redisClient.LPush(a.ctx, aggQueue, epochID).Err(); err != nil {
					log.WithError(err).Error("Failed to queue epoch for aggregation")
				} else {
					queuedEpochs[epochID] = true
					newBatchesFound++
					log.WithField("epoch", epochID).Info("Aggregator: Queued NEW epoch for aggregation")
				}
			}

			if len(activeEpochs) > 0 {
				log.WithFields(logrus.Fields{
					"active_epochs": len(activeEpochs),
					"finalized_batches": len(keys),
					"new_queued": newBatchesFound,
					"already_tracked": len(queuedEpochs) - newBatchesFound,
				}).Debug("Monitor check completed")
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
			// Count aggregated batches using ActiveEpochs set
			activeEpochsKey := a.keyBuilder.ActiveEpochs()
			activeEpochs, _ := a.redisClient.SMembers(a.ctx, activeEpochsKey).Result()

			aggregatedCount := 0
			for _, epochID := range activeEpochs {
				aggregatedKey := a.keyBuilder.BatchAggregated(epochID)
				exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
				if err == nil && exists > 0 {
					aggregatedCount++
				}
			}

			// Count active validators (non-namespaced keys for now)
			// This could be enhanced to use a namespaced active validators set in the future
			var validators []string
			// Use a simple pattern to get validator status keys
			// Note: This is a minor SCAN operation for monitoring only, not critical path
			cursor := uint64(0)
			for {
				scanKeys, nextCursor, err := a.redisClient.Scan(a.ctx, cursor, "validator:active:*", 100).Result()
				if err != nil {
					break
				}
				validators = append(validators, scanKeys...)
				cursor = nextCursor
				if cursor == 0 {
					break
				}
			}

			log.WithFields(logrus.Fields{
				"aggregated_batches": aggregatedCount,
				"active_validators":  len(validators),
			}).Info("Aggregator metrics")
		}
	}
}

func (a *Aggregator) Start() error {
	log.Info("Starting Aggregator")

	// Start queue-based processing
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

// extractValidatorIDs extracts validator IDs from incoming batch keys
func extractValidatorIDs(keys []string) []string {
	validators := make([]string, 0)
	for _, key := range keys {
		// Keys are in format: {protocol}:{market}:incoming:batch:{epochId}:{validatorId}
		parts := strings.Split(key, ":")
		if len(parts) >= 6 {
			validators = append(validators, parts[5])
		}
	}
	return validators
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