package vpa

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	rediskeys "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
)

// ValidatorPriorityAssigner binds to the VPA contract
type ValidatorPriorityAssigner struct {
	client       *ethclient.Client
	contractAddr common.Address
	abi          abi.ABI
	validator    common.Address
}

// PriorityInfo holds validator priority information
type PriorityInfo struct {
	EpochID     uint64
	Priority    int
	CanSubmit   bool
	WindowStart time.Time
	WindowEnd   time.Time
}

// ValidatorPriority represents a validator's priority assignment
type ValidatorPriority struct {
	ValidatorID string `json:"validatorId"`
	Priority    int    `json:"priority"`
	CanSubmit   bool   `json:"canSubmit"`
}

// PriorityMetadata holds metadata about priority assignment
type PriorityMetadata struct {
	EpochID       uint64    `json:"epochId"`
	Seed          string    `json:"seed"`
	Timestamp     time.Time `json:"timestamp"`
	ValidatorCount int      `json:"validatorCount"`
	DataMarket    string    `json:"dataMarket"`
}

// CachedPriorities holds all cached priorities for an epoch
type CachedPriorities struct {
	EpochID      uint64              `json:"epochId"`
	Metadata     PriorityMetadata    `json:"metadata"`
	Priorities   map[string]int      `json:"priorities"`   // validatorID -> priority
	TopValidator string              `json:"topValidator"`
	CachedAt     time.Time           `json:"cachedAt"`
}

// PriorityCachingClient wraps VPA client with Redis caching
type PriorityCachingClient struct {
	*ValidatorPriorityAssigner
	redisClient  *redis.Client
	keyBuilder   *rediskeys.KeyBuilder
	cacheTTL     time.Duration
	logger       *logrus.Entry
}

// NewPriorityCachingClient creates a new VPA client with Redis caching
func NewPriorityCachingClient(rpcURL, contractAddr, validatorAddr string,
	redisClient *redis.Client, protocolState, dataMarket string) (*PriorityCachingClient, error) {

	vpaClient, err := NewValidatorPriorityAssigner(rpcURL, contractAddr, validatorAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create VPA client: %w", err)
	}

	keyBuilder := rediskeys.NewKeyBuilder(protocolState, dataMarket)

	return &PriorityCachingClient{
		ValidatorPriorityAssigner: vpaClient,
		redisClient:               redisClient,
		keyBuilder:                keyBuilder,
		cacheTTL:                  24 * time.Hour, // Cache for 24 hours
		logger:                    logrus.WithField("component", "vpa-caching-client"),
	}, nil
}

// NewValidatorPriorityAssigner creates a new VPA contract client
func NewValidatorPriorityAssigner(rpcURL string, contractAddr string, validatorAddr string) (*ValidatorPriorityAssigner, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client: %w", err)
	}

	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid contract address: %s", contractAddr)
	}
	if !common.IsHexAddress(validatorAddr) {
		return nil, fmt.Errorf("invalid validator address: %s", validatorAddr)
	}

	// Load the VPA ABI (simplified version for our needs)
	vpaABI, err := abi.JSON(strings.NewReader(VPAABI))
	if err != nil {
		return nil, fmt.Errorf("failed to load VPA ABI: %w", err)
	}

	return &ValidatorPriorityAssigner{
		client:       client,
		contractAddr: common.HexToAddress(contractAddr),
		abi:          vpaABI,
		validator:    common.HexToAddress(validatorAddr),
	}, nil
}

// CanValidatorSubmit checks if this validator can submit for the given epoch and data market
func (vpa *ValidatorPriorityAssigner) CanValidatorSubmit(ctx context.Context, dataMarketAddr string, epochID uint64) (bool, error) {
	if !common.IsHexAddress(dataMarketAddr) {
		return false, fmt.Errorf("invalid data market address: %s", dataMarketAddr)
	}

	// Create call data for canValidatorSubmit
	data, err := vpa.abi.Pack("canValidatorSubmit",
		common.HexToAddress(dataMarketAddr),
		big.NewInt(int64(epochID)),
		vpa.validator)
	if err != nil {
		return false, fmt.Errorf("failed to pack canValidatorSubmit call: %w", err)
	}

	// Call the contract
	msg := ethereum.CallMsg{
		To:   &vpa.contractAddr,
		From: vpa.validator,
		Data: data,
	}
	result, err := vpa.client.CallContract(ctx, msg, nil)
	if err != nil {
		return false, fmt.Errorf("failed to call canValidatorSubmit: %w", err)
	}

	// Unpack the result
	var canSubmit bool
	err = vpa.abi.UnpackIntoInterface(&canSubmit, "canValidatorSubmit", result)
	if err != nil {
		return false, fmt.Errorf("failed to unpack canValidatorSubmit result: %w", err)
	}

	return canSubmit, nil
}

// GetMyPriority gets this validator's priority for the given epoch and data market
func (vpa *ValidatorPriorityAssigner) GetMyPriority(ctx context.Context, dataMarketAddr string, epochID uint64) (int, error) {
	if !common.IsHexAddress(dataMarketAddr) {
		return 0, fmt.Errorf("invalid data market address: %s", dataMarketAddr)
	}

	// First get the validator ID from the ValidatorState contract
	validatorID, err := vpa.getValidatorID(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get validator ID: %w", err)
	}

	// Create call data for getHistoricalPriority
	data, err := vpa.abi.Pack("getHistoricalPriority",
		common.HexToAddress(dataMarketAddr),
		big.NewInt(int64(epochID)),
		big.NewInt(int64(validatorID)))
	if err != nil {
		return 0, fmt.Errorf("failed to pack getHistoricalPriority call: %w", err)
	}

	// Call the contract
	msg := ethereum.CallMsg{
		To:   &vpa.contractAddr,
		From: vpa.validator,
		Data: data,
	}
	result, err := vpa.client.CallContract(ctx, msg, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to call getHistoricalPriority: %w", err)
	}

	// Unpack the result
	var priority *big.Int
	err = vpa.abi.UnpackIntoInterface(&priority, "getHistoricalPriority", result)
	if err != nil {
		return 0, fmt.Errorf("failed to unpack getHistoricalPriority result: %w", err)
	}

	if priority == nil {
		return 0, fmt.Errorf("no priority assigned for validator %d in epoch %d", validatorID, epochID)
	}

	return int(priority.Int64()), nil
}

// getValidatorID gets the validator ID from the ValidatorState contract
// This is a simplified version - in production you'd call the ValidatorState contract directly
func (vpa *ValidatorPriorityAssigner) getValidatorID(ctx context.Context) (uint64, error) {
	// For now, use a simple hash of the validator address as ID
	// In production, this should call validatorToNodeId on the ValidatorState contract
	hash := crypto.Keccak256Hash(vpa.validator.Bytes())
	hashBigInt := new(big.Int).SetBytes(hash[:])
	return uint64(hashBigInt.Uint64() % 1000), nil // Simple ID generation
}

// GetPriorityInfo gets comprehensive priority information for the validator
func (vpa *ValidatorPriorityAssigner) GetPriorityInfo(ctx context.Context, dataMarketAddr string, epochID uint64) (*PriorityInfo, error) {
	canSubmit, err := vpa.CanValidatorSubmit(ctx, dataMarketAddr, epochID)
	if err != nil {
		return nil, fmt.Errorf("failed to check canValidatorSubmit: %w", err)
	}

	priority, err := vpa.GetMyPriority(ctx, dataMarketAddr, epochID)
	if err != nil {
		return nil, fmt.Errorf("failed to get priority: %w", err)
	}

	// Calculate submission window times (simplified - should get from DataMarket contract)
	now := time.Now()
	windowStart := now.Add(-1 * time.Minute) // Assume window opened 1 minute ago
	windowEnd := now.Add(4 * time.Minute)   // Assume window closes in 4 minutes

	return &PriorityInfo{
		EpochID:     epochID,
		Priority:    priority,
		CanSubmit:   canSubmit,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}, nil
}

// IsTopPriority checks if this validator is the top priority (priority 1)
func (vpa *ValidatorPriorityAssigner) IsTopPriority(ctx context.Context, dataMarketAddr string, epochID uint64) (bool, error) {
	priority, err := vpa.GetMyPriority(ctx, dataMarketAddr, epochID)
	if err != nil {
		return false, err
	}
	return priority == 1, nil
}

// WaitForSubmissionWindow waits until the validator can submit
func (vpa *ValidatorPriorityAssigner) WaitForSubmissionWindow(ctx context.Context, dataMarketAddr string, epochID uint64) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			canSubmit, err := vpa.CanValidatorSubmit(ctx, dataMarketAddr, epochID)
			if err != nil {
				logrus.WithError(err).Debug("Failed to check submission eligibility")
				continue
			}
			if canSubmit {
				logrus.Info("✅ Submission window is open for this validator")
				return nil
			}
		}
	}
}

// Close closes the client connection
func (vpa *ValidatorPriorityAssigner) Close() {
	if vpa.client != nil {
		vpa.client.Close()
	}
}

// VPAABI contains the simplified ABI for the ValidatorPriorityAssigner contract
const VPAABI = `[
	{
		"inputs": [
			{"internalType": "address", "name": "dataMarket", "type": "address"},
			{"internalType": "uint256", "name": "epochId", "type": "uint256"},
			{"internalType": "address", "name": "validatorAddress", "type": "address"}
		],
		"name": "canValidatorSubmit",
		"outputs": [
			{"internalType": "bool", "name": "canSubmit", "type": "bool"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType": "address", "name": "dataMarket", "type": "address"},
			{"internalType": "uint256", "name": "epochId", "type": "uint256"},
			{"internalType": "uint256", "name": "validatorId", "type": "uint256"}
		],
		"name": "getHistoricalPriority",
		"outputs": [
			{"internalType": "uint256", "name": "", "type": "uint256"}
		],
		"stateMutability": "view",
		"type": "function"
	}
]`
// CacheEpochPriorities caches all validator priorities for an epoch
func (pcc *PriorityCachingClient) CacheEpochPriorities(ctx context.Context, dataMarket string, epochID uint64) error {
	pcc.logger.WithFields(logrus.Fields{
		"epochID":    epochID,
		"dataMarket": dataMarket,
	}).Info("Caching VPA priorities for epoch")

	// Get priorities from VPA contract
	priorities, metadata, err := pcc.getHistoricalPrioritiesFromContract(ctx, dataMarket, epochID)
	if err != nil {
		return fmt.Errorf("failed to get historical priorities: %w", err)
	}

	// Prepare cached data
	cachedData := CachedPriorities{
		EpochID:      epochID,
		Metadata:     metadata,
		Priorities:   priorities,
		TopValidator: pcc.findTopValidator(priorities),
		CachedAt:     time.Now(),
	}

	// Cache in Redis
	return pcc.storeCachedPriorities(ctx, epochID, &cachedData)
}

// GetValidatorPriority gets priority for a specific validator (cache-first)
func (pcc *PriorityCachingClient) GetValidatorPriority(ctx context.Context, dataMarket string, epochID uint64, validatorID string) (int, error) {
	// Try cache first
	if priority, err := pcc.getValidatorPriorityFromCache(ctx, epochID, validatorID); err == nil {
		return priority, nil
	}

	// Fallback to contract call
	pcc.logger.WithFields(logrus.Fields{
		"epochID":    epochID,
		"validatorID": validatorID,
	}).Warn("Cache miss, falling back to contract call")

	return pcc.getHistoricalPriorityFromContract(ctx, dataMarket, epochID, validatorID)
}

// IsTopPriority checks if validator has top priority for the epoch
func (pcc *PriorityCachingClient) IsTopPriority(ctx context.Context, dataMarket string, epochID uint64, validatorID string) (bool, error) {
	// Get top validator from cache
	topValidatorKey := pcc.keyBuilder.VPATopValidator(strconv.FormatUint(epochID, 10))
	topValidator, err := pcc.redisClient.Get(ctx, topValidatorKey).Result()
	if err == nil && topValidator == validatorID {
		return true, nil
	}

	// Fallback to checking all priorities
	priority, err := pcc.GetValidatorPriority(ctx, dataMarket, epochID, validatorID)
	if err != nil {
		return false, err
	}

	return priority == 1, nil // Priority 1 is top priority
}

// getHistoricalPrioritiesFromContract fetches all priorities from VPA contract
func (pcc *PriorityCachingClient) getHistoricalPrioritiesFromContract(ctx context.Context, dataMarket string, epochID uint64) (map[string]int, PriorityMetadata, error) {
	// This is a placeholder - actual implementation would call VPA contract methods
	// like getHistoricalPriorities() and getHistoricalValidatorCount()

	// For now, return empty data
	priorities := make(map[string]int)
	metadata := PriorityMetadata{
		EpochID:        epochID,
		Seed:           "",
		Timestamp:      time.Now(),
		ValidatorCount: 0,
		DataMarket:     dataMarket,
	}

	return priorities, metadata, fmt.Errorf("contract calls not yet implemented")
}

// storeCachedPriorities stores cached priorities in Redis
func (pcc *PriorityCachingClient) storeCachedPriorities(ctx context.Context, epochID uint64, data *CachedPriorities) error {
	epochIDStr := strconv.FormatUint(epochID, 10)

	// Store full priorities object
	prioritiesJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal cached priorities: %w", err)
	}

	prioritiesKey := pcc.keyBuilder.VPAPriorities(epochIDStr)
	if err := pcc.redisClient.Set(ctx, prioritiesKey, prioritiesJSON, pcc.cacheTTL).Err(); err != nil {
		return fmt.Errorf("failed to cache priorities: %w", err)
	}

	// Store individual validator priorities for quick lookup
	for validatorID, priority := range data.Priorities {
		validatorKey := pcc.keyBuilder.VPAValidatorPriority(epochIDStr, validatorID)
		if err := pcc.redisClient.Set(ctx, validatorKey, priority, pcc.cacheTTL).Err(); err != nil {
			pcc.logger.WithError(err).Warn("Failed to cache validator priority")
		}
	}

	// Store top validator
	if data.TopValidator != "" {
		topValidatorKey := pcc.keyBuilder.VPATopValidator(epochIDStr)
		if err := pcc.redisClient.Set(ctx, topValidatorKey, data.TopValidator, pcc.cacheTTL).Err(); err != nil {
			pcc.logger.WithError(err).Warn("Failed to cache top validator")
		}
	}

	pcc.logger.WithFields(logrus.Fields{
		"epochID":        epochID,
		"validatorCount": len(data.Priorities),
		"topValidator":   data.TopValidator,
	}).Info("Successfully cached VPA priorities")

	return nil
}

// getValidatorPriorityFromCache gets validator priority from Redis cache
func (pcc *PriorityCachingClient) getValidatorPriorityFromCache(ctx context.Context, epochID uint64, validatorID string) (int, error) {
	epochIDStr := strconv.FormatUint(epochID, 10)
	validatorKey := pcc.keyBuilder.VPAValidatorPriority(epochIDStr, validatorID)

	priorityStr, err := pcc.redisClient.Get(ctx, validatorKey).Result()
	if err != nil {
		return -1, err // Cache miss
	}

	priority, err := strconv.Atoi(priorityStr)
	if err != nil {
		return -1, fmt.Errorf("invalid priority format: %w", err)
	}

	return priority, nil
}

// findTopValidator finds the validator with priority 1
func (pcc *PriorityCachingClient) findTopValidator(priorities map[string]int) string {
	for validatorID, priority := range priorities {
		if priority == 1 {
			return validatorID
		}
	}
	return ""
}

// getHistoricalPriorityFromContract fetches priority from VPA contract (placeholder)
func (pcc *PriorityCachingClient) getHistoricalPriorityFromContract(ctx context.Context, dataMarket string, epochID uint64, validatorID string) (int, error) {
	// Placeholder for actual contract call implementation
	return -1, fmt.Errorf("contract call not yet implemented")
}
