package eventmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/go-redis/redis/v8"
	rpchelper "github.com/powerloom/go-rpc-helper"
	rediskeys "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	log "github.com/sirupsen/logrus"
)

// EventMonitor watches for EpochReleased events and manages submission windows
type EventMonitor struct {
	rpcHelper       *rpchelper.RPCHelper
	redisClient     *redis.Client
	contractAddr    common.Address
	contractABI     *ContractABI
	
	// Window management
	windowManager   *WindowManager
	
	// Event tracking
	lastProcessedBlock uint64
	eventChan          chan *EpochReleasedEvent
	epochReleasedSig   common.Hash // Cache the event signature to avoid recomputing
	
	// Configuration
	pollInterval    time.Duration
	windowDuration  time.Duration
	dataMarkets     []string // List of data market addresses to monitor
	
	ctx    context.Context
	cancel context.CancelFunc
}

// EpochReleasedEvent represents an epoch release from the protocol state contract
type EpochReleasedEvent struct {
	EpochID           *big.Int
	EpochEnd          *big.Int
	DataMarketAddress common.Address
	Timestamp         uint64
	TransactionHash   common.Hash
	BlockNumber       uint64
}

// WindowManager manages submission windows for epochs
type WindowManager struct {
	activeWindows   map[string]*EpochWindow // key: dataMarketAddress:epochID
	mu              sync.RWMutex
	windowSemaphore chan struct{}
	maxWindows      int
	redisClient     *redis.Client
	protocolState   string // Protocol state contract address for namespacing
	finalizationBatchSize int // Number of projects per finalization batch
	keyBuilders     map[string]*rediskeys.KeyBuilder // Cache key builders per data market
}

// scanKeys uses SCAN instead of KEYS for production safety
func (wm *WindowManager) scanKeys(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	var cursor uint64

	for {
		scanKeys, nextCursor, err := wm.redisClient.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}

		keys = append(keys, scanKeys...)
		cursor = nextCursor

		if cursor == 0 {
			break
		}
	}

	return keys, nil
}

// EpochWindow represents an active submission window
type EpochWindow struct {
	EpochID           *big.Int
	DataMarketAddress string
	StartTime         time.Time
	WindowDuration    time.Duration
	Timer             *time.Timer
	Done              chan struct{}
	StartBlockNum     uint64
	EndBlockNum       uint64
}

// Config for EventMonitor
type Config struct {
	RPCHelper        *rpchelper.RPCHelper
	ContractAddress  string
	ContractABIPath  string   // Path to the contract ABI JSON file
	RedisClient      *redis.Client
	WindowDuration   time.Duration // Default window duration
	StartBlock       uint64
	PollInterval     time.Duration
	DataMarkets      []string // Data market addresses to monitor
	MaxWindows       int      // Max concurrent submission windows
	FinalizationBatchSize int  // Number of projects per finalization batch
}

// NewEventMonitor creates a new event monitor
func NewEventMonitor(cfg *Config) (*EventMonitor, error) {
	if cfg.RPCHelper == nil {
		return nil, fmt.Errorf("RPC helper is required")
	}
	
	// Load the contract ABI
	var contractABI *ContractABI
	if cfg.ContractABIPath != "" {
		abi, err := LoadContractABI(cfg.ContractABIPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load contract ABI: %w", err)
		}
		contractABI = abi
		log.Infof("✅ Loaded contract ABI from %s", cfg.ContractABIPath)
		
		// Verify the ABI has the EpochReleased event
		if !contractABI.HasEvent("EpochReleased") {
			return nil, fmt.Errorf("ABI does not contain EpochReleased event")
		}
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	// If StartBlock is 0, fetch current block to start from latest
	startBlock := cfg.StartBlock
	if startBlock == 0 {
		currentBlock, err := cfg.RPCHelper.BlockNumber(context.Background())
		if err != nil {
			log.Warnf("Failed to get current block for start, using 0: %v", err)
		} else {
			// Start from current block minus 1 (like legacy collector)
			if currentBlock > 0 {
				startBlock = currentBlock - 1
			}
			log.Infof("Starting event monitor from current block: %d", startBlock)
		}
	}
	
	// Compute and cache event signature once at startup
	var epochReleasedSig common.Hash
	if contractABI != nil {
		sig, err := contractABI.GetEventHash("EpochReleased")
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to get EpochReleased event hash from ABI: %w", err)
		}
		epochReleasedSig = sig
		log.Infof("✅ Using ABI-derived EpochReleased event signature: %s", epochReleasedSig.Hex())
	} else {
		// Fallback to hardcoded signature for backward compatibility
		epochReleasedSig = getEpochReleasedEventSignature()
		log.Infof("✅ Using hardcoded EpochReleased event signature: %s", epochReleasedSig.Hex())
	}
	
	windowManager := &WindowManager{
		activeWindows:   make(map[string]*EpochWindow),
		windowSemaphore: make(chan struct{}, cfg.MaxWindows),
		maxWindows:      cfg.MaxWindows,
		redisClient:     cfg.RedisClient,
		protocolState:   cfg.ContractAddress, // Use protocol state for namespacing
		finalizationBatchSize: cfg.FinalizationBatchSize,
		keyBuilders:     make(map[string]*rediskeys.KeyBuilder),
	}
	
	return &EventMonitor{
		rpcHelper:          cfg.RPCHelper,
		redisClient:        cfg.RedisClient,
		contractAddr:       common.HexToAddress(cfg.ContractAddress),
		contractABI:        contractABI,
		windowManager:      windowManager,
		windowDuration:     cfg.WindowDuration,
		lastProcessedBlock: startBlock,
		eventChan:          make(chan *EpochReleasedEvent, 100),
		epochReleasedSig:   epochReleasedSig, // Set the cached signature
		pollInterval:       cfg.PollInterval,
		dataMarkets:        cfg.DataMarkets,
		ctx:                ctx,
		cancel:             cancel,
	}, nil
}

// Start begins monitoring for events
func (m *EventMonitor) Start() error {
	log.Info("🚀 Starting event monitor...")
	
	// Start event processor
	go m.processEvents()
	
	// Start block poller
	go m.pollBlocks()
	
	return nil
}

// Stop gracefully shuts down the monitor
func (m *EventMonitor) Stop() {
	log.Info("🛑 Stopping event monitor...")
	m.cancel()
	m.windowManager.Shutdown()
}

// pollBlocks continuously polls for new blocks and events
func (m *EventMonitor) pollBlocks() {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkForNewEvents()
		}
	}
}

// checkForNewEvents queries for new EpochReleased events
func (m *EventMonitor) checkForNewEvents() {
	// Get current block
	currentBlock, err := m.rpcHelper.BlockNumber(m.ctx)
	if err != nil {
		log.Errorf("Failed to get current block: %v", err)
		return
	}
	
	// Don't scan if we're already up to date
	if m.lastProcessedBlock >= currentBlock {
		return
	}
	
	// Limit scan range to avoid overwhelming the node
	toBlock := m.lastProcessedBlock + 1000
	if toBlock > currentBlock {
		toBlock = currentBlock
	}
	
	// Use cached event signature (computed once at startup)
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(m.lastProcessedBlock + 1)),
		ToBlock:   big.NewInt(int64(toBlock)),
		Addresses: []common.Address{m.contractAddr},
		Topics:    [][]common.Hash{{m.epochReleasedSig}},
	}
	
	logs, err := m.rpcHelper.FilterLogs(m.ctx, query)
	if err != nil {
		log.Errorf("Failed to filter logs: %v", err)
		return
	}
	
	for _, vLog := range logs {
		event := m.parseEpochReleasedEvent(vLog)
		if event != nil {
			// Only process events for configured data markets
			if m.isValidDataMarket(event.DataMarketAddress.Hex()) {
				m.eventChan <- event
			}
		}
	}
	
	m.lastProcessedBlock = toBlock
}

// parseEpochReleasedEvent parses the log into an EpochReleasedEvent
func (m *EventMonitor) parseEpochReleasedEvent(vLog types.Log) *EpochReleasedEvent {
	// Event: EpochReleased(address indexed dataMarketAddress, uint256 indexed epochId, uint256 begin, uint256 end, uint256 timestamp)
	// topics[0] = event signature
	// topics[1] = dataMarketAddress (indexed)
	// topics[2] = epochId (indexed)
	// data contains: begin, end, timestamp (non-indexed)
	
	if len(vLog.Topics) < 3 {
		log.Warnf("Invalid EpochReleased event: expected at least 3 topics, got %d", len(vLog.Topics))
		return nil
	}
	
	// Parse indexed fields from topics
	dataMarketAddress := common.HexToAddress(vLog.Topics[1].Hex())
	epochID := new(big.Int).SetBytes(vLog.Topics[2].Bytes())
	
	// Parse non-indexed fields from data
	// The data contains: begin (uint256), end (uint256), timestamp (uint256)
	if len(vLog.Data) < 96 { // 3 * 32 bytes
		log.Warnf("Invalid EpochReleased event data: expected at least 96 bytes, got %d", len(vLog.Data))
		return nil
	}
	
	// Each uint256 is 32 bytes
	// begin := new(big.Int).SetBytes(vLog.Data[0:32])  // Not needed for our purposes
	// end := new(big.Int).SetBytes(vLog.Data[32:64])   // Not needed for our purposes
	timestamp := new(big.Int).SetBytes(vLog.Data[64:96])
	
	log.Debugf("Parsed EpochReleased event: DataMarket=%s, EpochID=%s, Timestamp=%s",
		dataMarketAddress.Hex(), epochID.String(), timestamp.String())
	
	return &EpochReleasedEvent{
		EpochID:           epochID,
		DataMarketAddress: dataMarketAddress,
		BlockNumber:       vLog.BlockNumber,
		TransactionHash:   vLog.TxHash,
		Timestamp:         timestamp.Uint64(),
	}
}

// isValidDataMarket checks if the data market is in our configured list
func (m *EventMonitor) isValidDataMarket(address string) bool {
	for _, market := range m.dataMarkets {
		if market == address {
			return true
		}
	}
	return false
}

// getEpochReleasedEventSignature computes the keccak256 hash of the event signature
func getEpochReleasedEventSignature() common.Hash {
	// Event signature: EpochReleased(address,uint256,uint256,uint256,uint256)
	// This matches the event definition:
	// event EpochReleased(
	//     address indexed dataMarketAddress,
	//     uint256 indexed epochId,
	//     uint256 begin,
	//     uint256 end,
	//     uint256 timestamp
	// );
	eventSignature := []byte("EpochReleased(address,uint256,uint256,uint256,uint256)")
	hash := crypto.Keccak256Hash(eventSignature)
	
	log.Debugf("Computed EpochReleased event signature: %s", hash.Hex())
	return hash
}

// processEvents handles incoming epoch events
func (m *EventMonitor) processEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case event := <-m.eventChan:
			m.handleEpochReleased(event)
		}
	}
}

// handleEpochReleased processes a new epoch release
func (m *EventMonitor) handleEpochReleased(event *EpochReleasedEvent) {
	log.Infof("📅 Epoch %s released for market %s at block %d", 
		event.EpochID, event.DataMarketAddress.Hex(), event.BlockNumber)
	
	// Skip old epochs whose windows would have already expired
	// This prevents filling up the window manager with historical epochs
	epochAge := time.Since(time.Unix(int64(event.Timestamp), 0))
	if epochAge > m.windowDuration*2 {
		log.Debugf("Skipping old epoch %s (age: %v, window duration: %v)", 
			event.EpochID, epochAge, m.windowDuration)
		return
	}
	
	// Store epoch info in Redis
	epochKey := fmt.Sprintf("epoch:%s:%s:info", event.DataMarketAddress.Hex(), event.EpochID.String())
	epochData := map[string]interface{}{
		"epoch_id":       event.EpochID.String(),
		"epoch_end":      event.EpochEnd.String(),
		"data_market":    event.DataMarketAddress.Hex(),
		"released_at":    event.Timestamp,
		"block_number":   event.BlockNumber,
		"tx_hash":        event.TransactionHash.Hex(),
		"window_start":   time.Now().Unix(),
		"window_end":     time.Now().Add(m.windowDuration).Unix(),
	}
	
	// Store with pipeline for efficiency
	pipe := m.redisClient.Pipeline()
	pipe.HMSet(m.ctx, epochKey, epochData)
	pipe.Expire(m.ctx, epochKey, 24*time.Hour)
	
	// Also add to active epochs set
	activeKey := fmt.Sprintf("active_epochs:%s", event.DataMarketAddress.Hex())
	pipe.SAdd(m.ctx, activeKey, event.EpochID.String())
	pipe.Expire(m.ctx, activeKey, 24*time.Hour)
	
	if _, err := pipe.Exec(m.ctx); err != nil {
		log.Errorf("Failed to store epoch info: %v", err)
	}
	
	// Start submission window
	if err := m.windowManager.StartSubmissionWindow(
		m.ctx,
		event.DataMarketAddress.Hex(),
		event.EpochID,
		m.windowDuration,
		event.BlockNumber,
	); err != nil {
		log.Errorf("Failed to start submission window: %v", err)
	}
}

// WindowManager methods

func (wm *WindowManager) StartSubmissionWindow(ctx context.Context, dataMarket string, epochID *big.Int, duration time.Duration, startBlock uint64) error {
	key := fmt.Sprintf("%s:%s", dataMarket, epochID.String())
	
	// Check if window already exists
	wm.mu.RLock()
	if _, exists := wm.activeWindows[key]; exists {
		wm.mu.RUnlock()
		return fmt.Errorf("window already active for epoch %s in market %s", epochID, dataMarket)
	}
	wm.mu.RUnlock()
	
	// Try to acquire semaphore
	select {
	case wm.windowSemaphore <- struct{}{}:
		// Got permission
	case <-time.After(1 * time.Second):
		return fmt.Errorf("too many active windows (%d), refusing new window", wm.GetActiveCount())
	}
	
	// Create window
	window := &EpochWindow{
		EpochID:           epochID,
		DataMarketAddress: dataMarket,
		StartTime:         time.Now(),
		WindowDuration:    duration,
		Done:              make(chan struct{}),
		StartBlockNum:     startBlock,
	}
	
	// Add to active windows
	wm.mu.Lock()
	wm.activeWindows[key] = window
	activeCount := len(wm.activeWindows)
	wm.mu.Unlock()
	
	// Start window timer
	window.Timer = time.AfterFunc(duration, func() {
		wm.closeWindow(dataMarket, epochID)
	})
	
	// Mark window as open in Redis - namespaced with protocol:market
	kb := wm.getKeyBuilder(dataMarket)
	windowKey := kb.EpochWindow(epochID.String())
	wm.redisClient.Set(context.Background(), windowKey, "open", duration)
	
	log.Infof("⏰ Submission window opened for epoch %s in market %s (duration: %v, active: %d)",
		epochID, dataMarket, duration, activeCount)

	// Add monitoring metrics for window open
	timestamp := time.Now().Unix()

	// Pipeline for monitoring metrics
	pipe := wm.redisClient.Pipeline()

	// 1. Add to epochs timeline
	pipe.ZAdd(context.Background(), "metrics:epochs:timeline", &redis.Z{
		Score:  float64(timestamp),
		Member: fmt.Sprintf("open:%s", epochID.String()),
	})

	// 2. Store epoch info with TTL
	epochInfoKey := fmt.Sprintf("metrics:epoch:%s:info", epochID.String())
	pipe.HSet(context.Background(), epochInfoKey, map[string]interface{}{
		"status":      "open",
		"start":       timestamp,
		"data_market": dataMarket,
		"duration":    duration.Seconds(),
		"start_block": startBlock,
	})
	pipe.Expire(context.Background(), epochInfoKey, 2*time.Hour)

	// 3. Publish state change
	pipe.Publish(context.Background(), "state:change", fmt.Sprintf("epoch:open:%s", epochID.String()))

	// Execute pipeline
	if _, err := pipe.Exec(context.Background()); err != nil {
		log.Debugf("Failed to write monitoring metrics: %v", err)
	}

	return nil
}

func (wm *WindowManager) closeWindow(dataMarket string, epochID *big.Int) {
	key := fmt.Sprintf("%s:%s", dataMarket, epochID.String())
	
	wm.mu.Lock()
	window, exists := wm.activeWindows[key]
	if !exists {
		wm.mu.Unlock()
		return
	}
	
	// Remove from active windows
	delete(wm.activeWindows, key)
	activeCount := len(wm.activeWindows)
	wm.mu.Unlock()
	
	// Release semaphore
	<-wm.windowSemaphore
	
	// Close the done channel
	close(window.Done)
	
	// Mark window as closed in Redis - namespaced with protocol:market
	ctx := context.Background()
	kb := wm.getKeyBuilder(dataMarket)
	windowKey := kb.EpochWindow(epochID.String())
	wm.redisClient.Set(ctx, windowKey, "closed", 1*time.Hour)
	
	// Trigger finalization
	wm.triggerFinalization(dataMarket, epochID, window.StartBlockNum)
	
	log.Infof("⏱️ Submission window closed for epoch %s in market %s (remaining: %d)",
		epochID, dataMarket, activeCount)

	// Add monitoring metrics for window close
	timestamp := time.Now().Unix()

	// Pipeline for monitoring metrics
	pipe := wm.redisClient.Pipeline()

	// 1. Add to epochs timeline
	pipe.ZAdd(context.Background(), "metrics:epochs:timeline", &redis.Z{
		Score:  float64(timestamp),
		Member: fmt.Sprintf("close:%s", epochID.String()),
	})

	// 2. Update epoch info (already has TTL from open)
	epochInfoKey := fmt.Sprintf("metrics:epoch:%s:info", epochID.String())
	pipe.HSet(context.Background(), epochInfoKey, map[string]interface{}{
		"status": "closed",
		"end":    timestamp,
	})

	// 3. Publish state change
	pipe.Publish(context.Background(), "state:change", fmt.Sprintf("epoch:closed:%s", epochID.String()))

	// Execute pipeline
	if _, err := pipe.Exec(context.Background()); err != nil {
		log.Debugf("Failed to write monitoring metrics: %v", err)
	}
}

func (wm *WindowManager) triggerFinalization(dataMarket string, epochID *big.Int, startBlock uint64) {
	// First, collect all submissions for this epoch from Redis
	submissions := wm.collectEpochSubmissions(dataMarket, epochID)
	
	// Split submissions into smaller batches for parallel processing
	batchSize := wm.finalizationBatchSize
	if batchSize <= 0 {
		batchSize = 20 // Default fallback
	}
	batches := wm.splitIntoBatches(submissions, batchSize)
	
	// Track batch metadata in Redis for aggregation worker
	ctx := context.Background()
	kb := wm.getKeyBuilder(dataMarket)
	batchMetaKey := fmt.Sprintf("%s:%s:epoch:%s:batch:meta",
		wm.protocolState, dataMarket, epochID.String())
	
	batchMeta := map[string]interface{}{
		"epoch_id":     epochID.String(),
		"total_batches": len(batches),
		"total_projects": len(submissions),
		"created_at":   time.Now().Unix(),
		"data_market":  dataMarket,
	}
	
	metaData, _ := json.Marshal(batchMeta)
	wm.redisClient.Set(ctx, batchMetaKey, metaData, 2*time.Hour)
	
	// Push each batch to finalization queue
	queueKey := kb.FinalizationQueue()
	log.Debugf("Pushing %d batches to finalization queue: %s", len(batches), queueKey)
	
	for i, batch := range batches {
		batchData := map[string]interface{}{
			"epoch_id":    epochID.String(),
			"batch_id":    i,
			"total_batches": len(batches),
			"projects":    batch,
			"data_market": dataMarket,
		}
		
		data, _ := json.Marshal(batchData)
		if err := wm.redisClient.LPush(ctx, queueKey, data).Err(); err != nil {
			log.Errorf("Failed to push batch %d to finalization queue: %v", i, err)
			continue
		}
		log.Debugf("Pushed batch %d to finalization queue: %s", i, queueKey)
	}
	
	log.Infof("🎯 Split epoch %s into %d batches (%d projects total, batch size %d) for parallel finalization", 
		epochID, len(batches), len(submissions), batchSize)
}

func (wm *WindowManager) splitIntoBatches(submissions map[string]interface{}, batchSize int) []map[string]interface{} {
	var batches []map[string]interface{}
	currentBatch := make(map[string]interface{})
	count := 0
	
	for projectID, submissionData := range submissions {
		currentBatch[projectID] = submissionData
		count++
		
		if count >= batchSize {
			batches = append(batches, currentBatch)
			currentBatch = make(map[string]interface{})
			count = 0
		}
	}
	
	// Add remaining projects as final batch
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}
	
	return batches
}

// collectEpochSubmissions retrieves all processed submissions for an epoch
func (wm *WindowManager) collectEpochSubmissions(dataMarket string, epochID *big.Int) map[string]interface{} {
	ctx := context.Background()

	// Get key builder for this data market
	kb := wm.getKeyBuilder(dataMarket)

	// Get all submission IDs from the epoch set (namespaced)
	// Format: {protocol}:{market}:epoch:{epochID}:processed
	epochKey := kb.EpochProcessed(epochID.String())
	log.Debugf("Looking for submissions with key: %s (protocolState=%s)", epochKey, wm.protocolState)
	submissionIDs, err := wm.redisClient.SMembers(ctx, epochKey).Result()
	if err != nil {
		log.Errorf("Failed to get submission IDs for epoch %s in market %s: %v",
			epochID, dataMarket, err)
		return make(map[string]interface{})
	}
	log.Debugf("Found %d submission IDs for epoch %s", len(submissionIDs), epochID)

	// Track CIDs per project with vote counts AND submitter details
	// Structure: map[projectID]map[CID]count
	projectVotes := make(map[string]map[string]int)
	// Track WHO submitted WHAT for challenges/proofs
	submissionMetadata := make(map[string][]map[string]interface{}) // projectID -> list of submissions

	for _, submissionID := range submissionIDs {
		// Get the processed submission data
		// Keys are formatted as: {protocol}:{market}:processed:{sequencer_id}:{submission_id}
		pattern := fmt.Sprintf("%s:%s:processed:*:%s",
			wm.protocolState, dataMarket, submissionID)
		keys, _ := wm.scanKeys(ctx, pattern)
		
		for _, key := range keys {
			// Extract validator ID from key: {protocol}:{market}:processed:{validator_id}:{submission_id}
			// Note: validatorID extraction removed as reported_by_validator field no longer needed

			data, err := wm.redisClient.Get(ctx, key).Result()
			if err != nil {
				continue
			}

			var submission map[string]interface{}
			if err := json.Unmarshal([]byte(data), &submission); err != nil {
				log.Errorf("Failed to unmarshal submission: %v", err)
				continue
			}

			if subData, ok := submission["Submission"].(map[string]interface{}); ok {
				if request, ok := subData["request"].(map[string]interface{}); ok {
					if projectID, ok := request["projectId"].(string); ok {
						if snapshotCID, ok := request["snapshotCid"].(string); ok {
							// Track vote counts
							if projectVotes[projectID] == nil {
								projectVotes[projectID] = make(map[string]int)
							}
							projectVotes[projectID][snapshotCID]++

							// Track submission metadata for challenges/proofs
							slotID := uint64(0)
							if slot, ok := request["slotId"].(float64); ok {
								slotID = uint64(slot)
							}

							// Extract submitter info from submission
							// EIP-712 signature verification is done by dequeuer during processing
							// The verified snapshotter EVM address is stored in SnapshotterAddr field
							submitterID := ""
							if addr, ok := submission["SnapshotterAddr"].(string); ok && addr != "" {
								submitterID = addr
							} else {
								// Missing SnapshotterAddr indicates signature verification failed in dequeuer
								// This submission should not have been processed - log error and skip
								log.Errorf("Missing SnapshotterAddr for submission %s (epoch=%d, project=%s, CID=%s) - signature verification may have failed",
									submissionID, submission["Submission"].(map[string]interface{})["request"].(map[string]interface{})["epochId"],
									projectID, snapshotCID)
								continue
							}

							signature := ""
							if sig, ok := subData["signature"].(string); ok {
								signature = sig
							}

							metadata := map[string]interface{}{
								"submitter_id": submitterID,
								"snapshot_cid": snapshotCID,
								"slot_id":      slotID,
								"signature":    signature,
								"timestamp":    time.Now().Unix(),
							}

							submissionMetadata[projectID] = append(submissionMetadata[projectID], metadata)
							log.Debugf("Found submission: project=%s, CID=%s, submitter=%s", projectID, snapshotCID, submitterID)
						} else {
							log.Warnf("No snapshotCid in request: %+v", request)
						}
					} else {
						log.Warnf("No projectId in request: %+v", request)
					}
				} else {
					log.Warnf("No request field in submission: %+v", subData)
				}
			} else {
				log.Warnf("No Submission field in data: %+v", submission)
			}
		}
	}
	
	projectSubmissions := make(map[string]interface{})
	for projectID, cidVotes := range projectVotes {
		projectSubmissions[projectID] = map[string]interface{}{
			"cid_votes": cidVotes,
			"total_submissions": len(cidVotes),
			"submission_metadata": submissionMetadata[projectID], // Add WHO submitted WHAT
		}
		
		totalVotes := 0
		for _, votes := range cidVotes {
			totalVotes += votes
		}
		log.Debugf("Project %s: Collected %d unique CIDs with %d total submissions from %d submitters", 
			projectID, len(cidVotes), totalVotes, len(submissionMetadata[projectID]))
	}
	
	// Store the collected batch in Redis for finalizer (namespaced)
	// Format: {protocol}:{market}:batch:ready:{epochID}
	batchKey := fmt.Sprintf("%s:%s:batch:ready:%s",
		wm.protocolState, dataMarket, epochID.String())
	batchData, _ := json.Marshal(projectSubmissions)
	wm.redisClient.Set(ctx, batchKey, batchData, 1*time.Hour)
	
	log.Infof("📦 Collected %d unique projects for epoch %s", 
		len(projectSubmissions), epochID)
	
	return projectSubmissions
}

func (wm *WindowManager) GetActiveCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.activeWindows)
}

// getKeyBuilder returns a cached KeyBuilder for the given data market
func (wm *WindowManager) getKeyBuilder(dataMarket string) *rediskeys.KeyBuilder {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if kb, exists := wm.keyBuilders[dataMarket]; exists {
		return kb
	}

	kb := rediskeys.NewKeyBuilder(wm.protocolState, dataMarket)
	wm.keyBuilders[dataMarket] = kb
	return kb
}

func (wm *WindowManager) IsWindowOpen(dataMarket string, epochID *big.Int) bool {
	key := fmt.Sprintf("%s:%s", dataMarket, epochID.String())
	wm.mu.RLock()
	_, exists := wm.activeWindows[key]
	wm.mu.RUnlock()
	return exists
}

func (wm *WindowManager) Shutdown() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	
	// Stop all timers
	for key, window := range wm.activeWindows {
		window.Timer.Stop()
		close(window.Done)
		delete(wm.activeWindows, key)
	}
}