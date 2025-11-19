package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/ipfs"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/protocol"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/submissions"
	rediskeys "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/utils"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/vpa"
	"github.com/redis/go-redis/v9"
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

	// Contract clients for on-chain integration
	vpaClient *vpa.PriorityCachingClient // Enhanced caching client
	protocolClient *protocol.ProtocolState

	// relayer-py integration
	relayerPyEndpoint string              // relayer-py service endpoint
	useNewContracts   bool               // Enable new contract submissions
	httpClient        *http.Client       // HTTP client for relayer communication

	// Track aggregation state
	epochBatches map[uint64]map[string]*consensus.FinalizedBatch // epochID -> validatorID -> batch
	epochTimers  map[uint64]*time.Timer                          // epochID -> aggregation window timer

	// Track submission state
	submissionState map[uint64]bool // epochID -> submitted

	mu sync.RWMutex
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

	// Initialize contract clients if on-chain submission is enabled
	var vpaClient *vpa.PriorityCachingClient
	var protocolClient *protocol.ProtocolState
	var err error

	if cfg.EnableOnChainSubmission {
		log.Info("🔗 Initializing on-chain submission contracts")

		// Fetch VPA address from NEW ProtocolState contract if not provided
		vpaContractAddr := common.HexToAddress(cfg.VPAContractAddress)
		if vpaContractAddr == (common.Address{}) {
			log.Infof("🔍 Fetching VPA address from NEW ProtocolState contract...")

			// Use shared VPA fetching function
			rpcURL := cfg.RPCNodes[0]
			fetchedVPAAddress, err := vpa.FetchVPAAddress(rpcURL, cfg.NewProtocolStateContract)
			if err != nil {
				log.Warnf("⚠️  Failed to fetch VPA address: %v", err)
				vpaContractAddr = common.Address{}
			} else {
				vpaContractAddr = fetchedVPAAddress
				log.Infof("✅ Successfully fetched VPA address: %s", vpaContractAddr.Hex())
			}
		}

		// Initialize VPA caching client with fetched address
		if vpaContractAddr != (common.Address{}) && cfg.VPAValidatorAddress != "" {
			// Use first RPC node for VPA
			rpcURL := cfg.RPCNodes[0]
			vpaClient, err = vpa.NewPriorityCachingClient(
				rpcURL, vpaContractAddr.Hex(), cfg.VPAValidatorAddress,
				redisClient, protocolState, dataMarket)
			if err != nil {
				cancel()
				return nil, fmt.Errorf("failed to initialize VPA caching client: %w", err)
			}
			log.Info("✅ VPA caching client initialized")
		} else {
			log.Warn("⚠️  VPA contract address or validator address not available")
		}

		// Initialize ProtocolState client
		if cfg.ProtocolStateContract != "" && len(cfg.DataMarketAddresses) > 0 {
			// Use first RPC node for ProtocolState
			rpcURL := cfg.RPCNodes[0]
			// Note: In production, validator private key should be securely managed
			validatorPrivateKey := cfg.ValidatorAddress // For now, use address as placeholder
			protocolClient, err = protocol.NewProtocolState(
				rpcURL, cfg.ProtocolStateContract, validatorPrivateKey, cfg.ChainID)
			if err != nil {
				cancel()
				return nil, fmt.Errorf("failed to initialize ProtocolState client: %w", err)
			}
			log.Info("✅ ProtocolState client initialized")
		} else {
			log.Warn("⚠️  ProtocolState contract or data market not configured")
		}
	}

	aggregator := &Aggregator{
		ctx:               ctx,
		cancel:            cancel,
		redisClient:       redisClient,
		ipfsClient:        ipfsClient,
		config:            cfg,
		keyBuilder:        keyBuilder,
		vpaClient:         vpaClient,
		protocolClient:    protocolClient,
		relayerPyEndpoint: cfg.RelayerPyEndpoint,
		useNewContracts:   cfg.UseNewContracts,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		epochBatches:      make(map[uint64]map[string]*consensus.FinalizedBatch),
		epochTimers:       make(map[uint64]*time.Timer),
		submissionState:   make(map[uint64]bool),
	}

	// Initialize stream consumer (mandatory for deterministic aggregation)
	if err := aggregator.initializeStreamConsumer(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize stream consumer: %w", err)
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

			// Stream notifications are mandatory - aggregation is triggered by stream messages
			// Just sleep briefly to prevent busy-waiting
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// parseEpochID parses epoch ID from various formats (string, scientific notation)
func parseEpochID(epochIDStr string) (uint64, error) {
	// Try standard integer parsing first
	epochID, err := strconv.ParseUint(epochIDStr, 10, 64)
	if err == nil {
		return epochID, nil
	}

	// If that fails, try parsing as float64 (for scientific notation)
	floatVal, err := strconv.ParseFloat(epochIDStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse epoch ID '%s' as integer or float: %w", epochIDStr, err)
	}

	// Convert float to uint64, checking for overflow
	if floatVal < 0 || floatVal > float64(^uint64(0)) {
		return 0, fmt.Errorf("epoch ID '%s' is out of valid uint64 range", epochIDStr)
	}

	return uint64(floatVal), nil
}

// startAggregationWindow initiates or extends the aggregation window for Level 2
func (a *Aggregator) startAggregationWindow(epochIDStr string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Convert to uint64 for map key
	epochID, err := parseEpochID(epochIDStr)
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
	epochID, err := parseEpochID(epochIDStr)
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

	if a.ipfsClient != nil {
		if cid, err := a.ipfsClient.StoreFinalizedBatch(a.ctx, finalizedBatch); err == nil {
			finalizedBatch.BatchIPFSCID = cid
		} else {
			log.WithError(err).Warn("Failed to store finalized batch in IPFS, continuing without CID")
		}
	}

	// Store as our local finalized batch (now with BatchIPFSCID populated)
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
	pipe.ZAdd(a.ctx, a.keyBuilder.MetricsBatchesTimeline(), redis.Z{
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
	pipe.SetEx(a.ctx, batchMetricsKey, string(jsonData), 24*time.Hour)

	// 3. Add to validator batches timeline
	validatorBatchesKey := a.keyBuilder.MetricsValidatorBatches(a.config.SequencerID)
	pipe.ZAdd(a.ctx, validatorBatchesKey, redis.Z{
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

	// Note: IPFS storage is now handled in the calling function (aggregateWorkerParts)
	// to ensure BatchIPFSCID is set before Redis persistence

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

	// Store in IPFS before Redis to ensure BatchIPFSCID is populated
	if a.ipfsClient != nil {
		if cid, err := a.ipfsClient.StoreFinalizedBatch(a.ctx, &aggregatedBatch); err == nil {
			aggregatedBatch.BatchIPFSCID = cid
			log.WithField("cid", cid).Info("Stored aggregated batch in IPFS")
		} else {
			log.WithError(err).Warn("Failed to store aggregated batch in IPFS, continuing without CID")
		}
	}

	// Store aggregated batch (now with BatchIPFSCID populated if IPFS was available)
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

	// Attempt new contract submission if enabled
	if a.useNewContracts {
		epochID, _ := strconv.ParseUint(epochIDStr, 10, 64)
		go a.handleNewContractSubmission(epochID, &aggregatedBatch)
	}

	// Add monitoring metrics for Level 2 aggregation
	timestamp := time.Now().Unix()
	epochID, _ := parseEpochID(epochIDStr)

	// Pipeline for monitoring metrics
	pipe := a.redisClient.Pipeline()

	// 1. Add to batches timeline
	pipe.ZAdd(a.ctx, a.keyBuilder.MetricsBatchesTimeline(), redis.Z{
		Score:  float64(timestamp),
		Member: fmt.Sprintf("aggregated:%s", utils.FormatEpochID(epochIDStr)),
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
	pipe.SetEx(a.ctx, batchMetricsKey, string(jsonData), 24*time.Hour)

	// 3. Store validator list with TTL (include local + remote validators)
	validatorsKey := a.keyBuilder.MetricsBatchValidators(epochIDStr)
	allValidators := extractValidatorIDs(incomingKeys)
	// Add local validator ID
	allValidators = append(allValidators, a.config.SequencerID)
	validatorList, _ := json.Marshal(allValidators)
	pipe.SetEx(a.ctx, validatorsKey, string(validatorList), 24*time.Hour)

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

	// Note: IPFS storage is now handled in the calling function (aggregateEpoch)
	// to ensure BatchIPFSCID is set before Redis persistence

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

			// Check for finalized batches using timeline entries (matches monitoring API)
			// Get recent aggregated entries from timeline
			timelineKey := a.keyBuilder.MetricsBatchesTimeline()
			timelineEntries, err := a.redisClient.ZRevRangeByScoreWithScores(a.ctx, timelineKey, &redis.ZRangeBy{
				Min:   strconv.FormatInt(time.Now().Unix()-3600, 10), // Last hour
				Max:   "+inf",
				Count: 1000,
			}).Result()
			if err != nil {
				log.WithError(err).Debug("Failed to get timeline entries")
				continue
			}

			// Count aggregated entries and get active epochs from timeline
			finalizedCount := 0
			keys := make([]string, 0)
			for _, entry := range timelineEntries {
				if strings.HasPrefix(entry.Member.(string), "aggregated:") {
					finalizedCount++
					epochID := strings.TrimPrefix(entry.Member.(string), "aggregated:")
					// Check if the aggregated batch exists
					aggregatedKey := a.keyBuilder.BatchAggregated(epochID)
					exists, err := a.redisClient.Exists(a.ctx, aggregatedKey).Result()
					if err == nil && exists > 0 {
						keys = append(keys, aggregatedKey)
					}
				}
			}

			newBatchesFound := 0
			// Process each recent aggregated epoch from timeline
			for _, entry := range timelineEntries {
				if !strings.HasPrefix(entry.Member.(string), "aggregated:") {
					continue
				}

				epochID := strings.TrimPrefix(entry.Member.(string), "aggregated:")

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

			if len(timelineEntries) > 0 {
				log.WithFields(logrus.Fields{
					"active_epochs": len(timelineEntries),
					"finalized_batches": finalizedCount,
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
			// Count aggregated batches using timeline entries (matches monitoring API)
			// This counts all finalized batches, not just active ones
			aggregatedCount := 0
			timelineKey := a.keyBuilder.MetricsBatchesTimeline()
			// Count entries with "aggregated:" prefix in timeline
			timelineEntries, err := a.redisClient.ZRange(a.ctx, timelineKey, 0, -1).Result()
			if err == nil {
				for _, entry := range timelineEntries {
					if strings.HasPrefix(entry, "aggregated:") {
						aggregatedCount++
					}
				}
			}

			// Count active validators using deterministic aggregation
			// Use ActiveEpochs and EpochValidators sets instead of SCAN
			var validators []string
			validatorSet := make(map[string]bool) // Use map to avoid duplicates

			// Get all active epochs
			activeEpochs, err := a.redisClient.SMembers(a.ctx, a.keyBuilder.ActiveEpochs()).Result()
			if err != nil {
				log.WithError(err).Debug("Failed to get active epochs for validator counting")
			} else {
				// Get validators from each active epoch
				for _, epochID := range activeEpochs {
					epochValidators, err := a.redisClient.SMembers(a.ctx, a.keyBuilder.EpochValidators(epochID)).Result()
					if err != nil {
						log.WithError(err).WithField("epoch", epochID).Debug("Failed to get epoch validators")
						continue
					}
					// Add validators to set to avoid duplicates
					for _, validatorID := range epochValidators {
						validatorSet[validatorID] = true
					}
				}
			}

			// Convert map to slice
			for validatorID := range validatorSet {
				validators = append(validators, validatorID)
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

// handleNewContractSubmission implements new contract submission logic
func (a *Aggregator) handleNewContractSubmission(epochID uint64, aggregatedBatch *consensus.FinalizedBatch) {
	// Convert epochID to string for consistency
	epochIDStr := strconv.FormatUint(epochID, 10)

	log.WithFields(logrus.Fields{
		"epoch":    epochIDStr,
		"projects": len(aggregatedBatch.ProjectIds),
	}).Info("🚀 Starting new contract submission")

	// Get new data market address for submission
	newDataMarket := a.config.NewDataMarket
	if newDataMarket == "" {
		log.WithField("epoch", epochIDStr).Error("No new data market address configured")
		return
	}

	// Check if already submitted
	a.mu.Lock()
	if a.submissionState[epochID] {
		a.mu.Unlock()
		log.WithField("epoch", epochIDStr).Info("Already submitted for this epoch")
		return
	}
	a.mu.Unlock()

	// Submit to new contracts via relayer-py
	if err := a.submitBatchViaRelayer(epochID, aggregatedBatch, newDataMarket); err != nil {
		log.WithError(err).Error("New contract submission failed")
		// Don't return error - new contract submission failure shouldn't affect other processing
	}
}

// submitBatchToContract submits the aggregated batch to the ProtocolState contract
func (a *Aggregator) submitBatchToContract(epochID uint64, aggregatedBatch *consensus.FinalizedBatch, dataMarketAddr string) {
	epochIDStr := strconv.FormatUint(epochID, 10)

	// Check if already submitted (double-check)
	a.mu.Lock()
	if a.submissionState[epochID] {
		a.mu.Unlock()
		log.WithField("epoch", epochIDStr).Info("Already submitted for this epoch")
		return
	}
	a.mu.Unlock()

	// Prepare batch submission
	submission := &protocol.BatchSubmission{
		EpochID:           epochID,
		BatchCID:          aggregatedBatch.BatchIPFSCID,
		ProjectIDs:        aggregatedBatch.ProjectIds,
		SnapshotCIDs:      aggregatedBatch.SnapshotCids,
		FinalizedCIDsRoot: aggregatedBatch.MerkleRoot,
		GasLimit:          2000000, // 2M gas limit
	}

	log.WithFields(logrus.Fields{
		"epoch":      epochIDStr,
		"batch_cid":  submission.BatchCID,
		"projects":   len(submission.ProjectIDs),
		"data_market": dataMarketAddr,
	}).Info("📤 Submitting batch to ProtocolState contract")

	// Submit asynchronously
	resultChan := a.protocolClient.SubmitBatchAsync(a.ctx, dataMarketAddr, submission)

	// Wait for result
	select {
	case result := <-resultChan:
		if result.Success {
			a.mu.Lock()
			a.submissionState[epochID] = true
			a.mu.Unlock()

			log.WithFields(logrus.Fields{
				"epoch":        epochIDStr,
				"tx_hash":      result.TxHash,
				"block_number": result.BlockNumber,
				"gas_used":     result.GasUsed,
			}).Info("✅ Batch submission successful")
		} else {
			log.WithFields(logrus.Fields{
				"epoch": epochIDStr,
				"error": result.Error,
			}).Error("❌ Batch submission failed")
		}
	case <-a.ctx.Done():
		log.WithField("epoch", epochIDStr).Info("Submission cancelled due to shutdown")
		return
	case <-time.After(2 * time.Minute):
		log.WithField("epoch", epochIDStr).Warn("⏰ Batch submission timeout")
	}
}

// submitBatchViaRelayer submits batch to new contracts via relayer-py service
func (a *Aggregator) submitBatchViaRelayer(epochID uint64, aggregatedBatch *consensus.FinalizedBatch, dataMarketAddr string) error {
	// Always check if we have VPA priority for this epoch
	if !a.hasVPAPriority(epochID, dataMarketAddr) {
		log.WithField("epoch", epochID).Debug("No VPA priority, skipping new contract submission")
		return nil
	}

	if a.relayerPyEndpoint == "" {
		log.WithField("epoch", epochID).Debug("Relayer endpoint not configured, skipping new contract submission")
		return nil
	}

	epochIDStr := strconv.FormatUint(epochID, 10)

	log.WithFields(logrus.Fields{
		"epoch":      epochIDStr,
		"projects":   len(aggregatedBatch.ProjectIds),
		"data_market": dataMarketAddr,
		"endpoint":    a.relayerPyEndpoint,
	}).Info("🚀 Submitting batch via relayer-py")

	// Prepare payload for relayer-py service
	payload := map[string]interface{}{
		"epoch_id":         epochID,
		"batch_cid":        aggregatedBatch.BatchIPFSCID,
		"project_ids":      aggregatedBatch.ProjectIds,
		"snapshot_cids":    aggregatedBatch.SnapshotCids,
		"merkle_root":      fmt.Sprintf("%x", aggregatedBatch.MerkleRoot),
		"validator_id":     a.config.SequencerID,
		"timestamp":        aggregatedBatch.Timestamp,
		"data_market":      dataMarketAddr,
		"protocol_state":   a.config.NewProtocolState,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal relayer-py submission payload: %w", err)
	}

	// Submit to relayer-py service
	endpoint := a.relayerPyEndpoint + "/submitSubmissionBatch"
	resp, err := a.httpClient.Post(endpoint, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to submit batch to relayer-py: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("VPA relayer returned non-200 status: %d", resp.StatusCode)
	}

	var response struct {
		Success   bool   `json:"success"`
		TxHash    string `json:"tx_hash"`
		BlockNumber uint64 `json:"block_number"`
		GasUsed   uint64 `json:"gas_used"`
		Error     string `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode VPA relayer response: %w", err)
	}

	if response.Success {
		log.WithFields(logrus.Fields{
			"epoch":        epochIDStr,
			"tx_hash":      response.TxHash,
			"block_number": response.BlockNumber,
			"gas_used":     response.GasUsed,
		}).Info("✅ VPA batch submission successful")

		// Mark as submitted for this epoch
		a.mu.Lock()
		a.submissionState[epochID] = true
		a.mu.Unlock()

		return nil
	} else {
		return fmt.Errorf("VPA batch submission failed: %s", response.Error)
	}
}

// checkBatchAlreadySubmitted performs a simple check if batch was already submitted
// This is a simplified version - in production you'd query the contract for submissions
func (a *Aggregator) checkBatchAlreadySubmitted(epochID uint64) bool {
	// Simple time-based check - if more than 30 seconds have passed since aggregation window
	// assume someone else might have submitted
	// In production, this should query the contract for epoch submission status

	// Check our local submission state first
	a.mu.Lock()
	submitted := a.submissionState[epochID]
	a.mu.Unlock()

	if submitted {
		return true
	}

	// For now, return false to allow submission
	// In production, implement proper contract querying
	return false
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

// hasVPAPriority checks if this validator has VPA priority for the given epoch and data market
func (a *Aggregator) hasVPAPriority(epochID uint64, dataMarketAddr string) bool {
	if a.vpaClient == nil {
		log.Debug("VPA client not initialized, cannot check priority")
		return false
	}

	// Get our validator's priority
	priority, err := a.vpaClient.GetMyPriority(a.ctx, dataMarketAddr, epochID)
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"epoch":      epochID,
			"data_market": dataMarketAddr,
		}).Debug("Failed to get VPA priority")
		return false
	}

	// Check if we have a valid priority (1 = highest priority)
	hasPriority := priority > 0

	log.WithFields(logrus.Fields{
		"epoch":      epochID,
		"data_market": dataMarketAddr,
		"priority":   priority,
		"has_priority": hasPriority,
	}).Debug("VPA priority check result")

	return hasPriority
}