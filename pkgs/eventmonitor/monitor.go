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
	"github.com/go-redis/redis/v8"
	rpchelper "github.com/powerloom/go-rpc-helper"
	log "github.com/sirupsen/logrus"
)

// EventMonitor watches for EpochReleased events and manages submission windows
type EventMonitor struct {
	rpcHelper       *rpchelper.RPCHelper
	redisClient     *redis.Client
	contractAddr    common.Address
	
	// Window management
	windowManager   *WindowManager
	
	// Event tracking
	lastProcessedBlock uint64
	eventChan          chan *EpochReleasedEvent
	
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
	RedisClient      *redis.Client
	WindowDuration   time.Duration // Default window duration
	StartBlock       uint64
	PollInterval     time.Duration
	DataMarkets      []string // Data market addresses to monitor
	MaxWindows       int      // Max concurrent submission windows
}

// NewEventMonitor creates a new event monitor
func NewEventMonitor(cfg *Config) (*EventMonitor, error) {
	if cfg.RPCHelper == nil {
		return nil, fmt.Errorf("RPC helper is required")
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	windowManager := &WindowManager{
		activeWindows:   make(map[string]*EpochWindow),
		windowSemaphore: make(chan struct{}, cfg.MaxWindows),
		maxWindows:      cfg.MaxWindows,
		redisClient:     cfg.RedisClient,
		protocolState:   cfg.ContractAddress, // Use protocol state for namespacing
	}
	
	return &EventMonitor{
		rpcHelper:          cfg.RPCHelper,
		redisClient:        cfg.RedisClient,
		contractAddr:       common.HexToAddress(cfg.ContractAddress),
		windowManager:      windowManager,
		windowDuration:     cfg.WindowDuration,
		lastProcessedBlock: cfg.StartBlock,
		eventChan:          make(chan *EpochReleasedEvent, 100),
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
	
	// Create filter query for EpochReleased events
	// TODO: Replace with actual event signature from contract ABI
	epochReleasedSig := common.HexToHash("0x...") // Placeholder - need actual signature
	
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(m.lastProcessedBlock + 1)),
		ToBlock:   big.NewInt(int64(toBlock)),
		Addresses: []common.Address{m.contractAddr},
		Topics:    [][]common.Hash{{epochReleasedSig}},
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
	// TODO: Implement actual parsing based on contract ABI
	// This is a placeholder - need to parse topics and data
	
	if len(vLog.Topics) < 3 {
		return nil
	}
	
	return &EpochReleasedEvent{
		EpochID:           big.NewInt(0), // Parse from topics[1]
		DataMarketAddress: common.HexToAddress("0x0"), // Parse from topics[2]
		BlockNumber:       vLog.BlockNumber,
		TransactionHash:   vLog.TxHash,
		Timestamp:         uint64(time.Now().Unix()),
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
	
	// Mark window as open in Redis
	windowKey := fmt.Sprintf("epoch:%s:%s:window", dataMarket, epochID.String())
	wm.redisClient.Set(context.Background(), windowKey, "open", duration)
	
	log.Infof("⏰ Submission window opened for epoch %s in market %s (duration: %v, active: %d)",
		epochID, dataMarket, duration, activeCount)
	
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
	
	// Mark window as closed in Redis
	ctx := context.Background()
	windowKey := fmt.Sprintf("epoch:%s:%s:window", dataMarket, epochID.String())
	wm.redisClient.Set(ctx, windowKey, "closed", 1*time.Hour)
	
	// Trigger finalization
	wm.triggerFinalization(dataMarket, epochID, window.StartBlockNum)
	
	log.Infof("⏱️ Submission window closed for epoch %s in market %s (remaining: %d)",
		epochID, dataMarket, activeCount)
}

func (wm *WindowManager) triggerFinalization(dataMarket string, epochID *big.Int, startBlock uint64) {
	// First, collect all submissions for this epoch from Redis
	submissions := wm.collectEpochSubmissions(dataMarket, epochID)
	
	// Push to finalization queue with submission data (namespaced)
	finalizationData := map[string]interface{}{
		"protocol_state":   wm.protocolState,
		"data_market":      dataMarket,
		"epoch_id":         epochID.String(),
		"closed_at":        time.Now().Unix(),
		"start_block":      startBlock,
		"trigger":          "window_timeout",
		"submission_count": len(submissions),
	}
	
	// Use namespaced finalization queue
	// Format: {protocol}:{market}:finalizationQueue
	queueKey := fmt.Sprintf("%s:%s:finalizationQueue", wm.protocolState, dataMarket)
	
	data, _ := json.Marshal(finalizationData)
	if err := wm.redisClient.LPush(context.Background(), queueKey, data).Err(); err != nil {
		log.Errorf("Failed to trigger finalization: %v", err)
		return
	}
	
	log.Infof("🎯 Triggered finalization for epoch %s in market %s (collected %d submissions)", 
		epochID, dataMarket, len(submissions))
}

// collectEpochSubmissions retrieves all processed submissions for an epoch
func (wm *WindowManager) collectEpochSubmissions(dataMarket string, epochID *big.Int) map[string]interface{} {
	ctx := context.Background()
	
	// Get protocol state from config (assuming it's stored in window manager)
	protocolState := wm.protocolState // You'll need to add this field
	
	// Get all submission IDs from the epoch set (namespaced)
	// Format: {protocol}:{market}:epoch:{epochID}:processed
	epochKey := fmt.Sprintf("%s:%s:epoch:%s:processed", 
		protocolState, dataMarket, epochID.String())
	submissionIDs, err := wm.redisClient.SMembers(ctx, epochKey).Result()
	if err != nil {
		log.Errorf("Failed to get submission IDs for epoch %s in market %s: %v", 
			epochID, dataMarket, err)
		return make(map[string]interface{})
	}
	
	// Map to store project ID -> CID mappings
	projectSubmissions := make(map[string]interface{})
	
	for _, submissionID := range submissionIDs {
		// Get the processed submission data
		// Keys are formatted as: {protocol}:{market}:processed:{sequencer_id}:{submission_id}
		pattern := fmt.Sprintf("%s:%s:processed:*:%s", 
			protocolState, dataMarket, submissionID)
		keys, _ := wm.redisClient.Keys(ctx, pattern).Result()
		
		for _, key := range keys {
			data, err := wm.redisClient.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			
			var submission map[string]interface{}
			if err := json.Unmarshal([]byte(data), &submission); err != nil {
				continue
			}
			
			// Extract project ID and CID
			if subData, ok := submission["submission"].(map[string]interface{}); ok {
				if request, ok := subData["request"].(map[string]interface{}); ok {
					projectID := request["projectId"]
					snapshotCID := request["snapshotCid"]
					
					if projectID != nil && snapshotCID != nil {
						// Store mapping (could have multiple submissions per project)
						projectSubmissions[projectID.(string)] = snapshotCID
					}
				}
			}
		}
	}
	
	// Store the collected batch in Redis for finalizer (namespaced)
	// Format: {protocol}:{market}:batch:ready:{epochID}
	batchKey := fmt.Sprintf("%s:%s:batch:ready:%s", 
		protocolState, dataMarket, epochID.String())
	batchData, _ := json.Marshal(projectSubmissions)
	wm.redisClient.Set(ctx, batchKey, batchData, 1*time.Hour)
	
	log.Infof("📦 Collected %d unique project submissions for epoch %s", 
		len(projectSubmissions), epochID)
	
	return projectSubmissions
}

func (wm *WindowManager) GetActiveCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.activeWindows)
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