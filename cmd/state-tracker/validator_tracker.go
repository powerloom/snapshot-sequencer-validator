package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

// ValidatorTracker manages validator-specific state tracking
type ValidatorTracker struct {
	tracker *StateTracker
}

// NewValidatorTracker creates a new validator tracker
func NewValidatorTracker(tracker *StateTracker) *ValidatorTracker {
	return &ValidatorTracker{tracker: tracker}
}

// ProcessEvent handles validator-related events
func (vt *ValidatorTracker) ProcessEvent(ctx context.Context, event *Event) {
	switch event.Type {
	case "validator_online":
		vt.handleValidatorOnline(ctx, event)
	case "validator_offline":
		vt.handleValidatorOffline(ctx, event)
	case "validator_active":
		vt.handleValidatorActive(ctx, event)
	case "validator_inactive":
		vt.handleValidatorInactive(ctx, event)
	case "validator_batch_submitted":
		vt.handleBatchSubmitted(ctx, event)
	}
}

// handleValidatorOnline processes validator coming online
func (vt *ValidatorTracker) handleValidatorOnline(ctx context.Context, event *Event) {
	validatorID := event.EntityID
	metadata := map[string]interface{}{
		"online_at":   event.Timestamp,
		"peer_id":     event.Data["peer_id"],
		"multiaddr":   event.Data["multiaddr"],
		"version":     event.Data["version"],
	}

	// Update state to online
	if err := vt.tracker.UpdateState(ctx, "validator", validatorID, ValidatorStateOnline, metadata); err != nil {
		log.WithError(err).WithField("validator_id", validatorID).Error("Failed to update validator state")
		return
	}

	// Add to online validators
	vt.tracker.redis.SAdd(ctx, "online:validators", validatorID)
	vt.tracker.redis.SRem(ctx, "offline:validators", validatorID)

	// Add to time index
	vt.tracker.redis.ZAdd(ctx, "metrics:validators:by_time", &redis.Z{
		Score:  float64(event.Timestamp),
		Member: validatorID,
	})

	// Track last seen
	vt.tracker.redis.HSet(ctx, "validators:last_seen", validatorID, event.Timestamp)

	log.WithField("validator_id", validatorID).Info("Validator came online")
}

// handleValidatorOffline processes validator going offline
func (vt *ValidatorTracker) handleValidatorOffline(ctx context.Context, event *Event) {
	validatorID := event.EntityID
	metadata := map[string]interface{}{
		"offline_at": event.Timestamp,
		"reason":     event.Data["reason"],
	}

	// Update state to offline
	if err := vt.tracker.UpdateState(ctx, "validator", validatorID, ValidatorStateOffline, metadata); err != nil {
		log.WithError(err).WithField("validator_id", validatorID).Error("Failed to update validator state")
		return
	}

	// Move to offline validators
	pipe := vt.tracker.redis.Pipeline()
	pipe.SRem(ctx, "online:validators", validatorID)
	pipe.SRem(ctx, "active:validators", validatorID)
	pipe.SAdd(ctx, "offline:validators", validatorID)
	pipe.Exec(ctx)

	// Update active validators gauge
	activeCount, _ := vt.tracker.redis.SCard(ctx, "active:validators").Result()
	activeEntities.WithLabelValues("validator").Set(float64(activeCount))

	log.WithFields(logrus.Fields{
		"validator_id": validatorID,
		"reason":       event.Data["reason"],
	}).Info("Validator went offline")
}

// handleValidatorActive processes validator becoming active (participating)
func (vt *ValidatorTracker) handleValidatorActive(ctx context.Context, event *Event) {
	validatorID := event.EntityID
	metadata := map[string]interface{}{
		"active_at":    event.Timestamp,
		"epoch_id":     event.Data["epoch_id"],
		"stake_amount": event.Data["stake_amount"],
	}

	// Update state to active
	if err := vt.tracker.UpdateState(ctx, "validator", validatorID, ValidatorStateActive, metadata); err != nil {
		log.WithError(err).WithField("validator_id", validatorID).Error("Failed to update validator state")
		return
	}

	// Add to active validators
	vt.tracker.redis.SAdd(ctx, "active:validators", validatorID)

	// Link to epoch if provided
	if epochID, ok := event.Data["epoch_id"].(string); ok && epochID != "" {
		vt.tracker.redis.SAdd(ctx, fmt.Sprintf("epoch:%s:validators", epochID), validatorID)
		vt.tracker.redis.SAdd(ctx, fmt.Sprintf("validator:%s:epochs", validatorID), epochID)
	}

	// Track participation
	vt.tracker.redis.HIncrBy(ctx, fmt.Sprintf("validator:%s:stats", validatorID), "epochs_participated", 1)
	vt.tracker.redis.HSet(ctx, fmt.Sprintf("validator:%s:stats", validatorID), "last_active", event.Timestamp)

	// Update active validators gauge
	activeCount, _ := vt.tracker.redis.SCard(ctx, "active:validators").Result()
	activeEntities.WithLabelValues("validator").Set(float64(activeCount))

	log.WithField("validator_id", validatorID).Debug("Validator became active")
}

// handleValidatorInactive processes validator becoming inactive
func (vt *ValidatorTracker) handleValidatorInactive(ctx context.Context, event *Event) {
	validatorID := event.EntityID
	metadata := map[string]interface{}{
		"inactive_at": event.Timestamp,
		"reason":      event.Data["reason"],
	}

	// Update state to inactive
	if err := vt.tracker.UpdateState(ctx, "validator", validatorID, ValidatorStateInactive, metadata); err != nil {
		log.WithError(err).WithField("validator_id", validatorID).Error("Failed to update validator state")
		return
	}

	// Remove from active validators
	vt.tracker.redis.SRem(ctx, "active:validators", validatorID)

	// Track inactivity reason
	if reason, ok := event.Data["reason"].(string); ok && reason != "" {
		vt.tracker.redis.HIncrBy(ctx, "stats:inactivity_reasons", reason, 1)
	}

	// Update active validators gauge
	activeCount, _ := vt.tracker.redis.SCard(ctx, "active:validators").Result()
	activeEntities.WithLabelValues("validator").Set(float64(activeCount))

	log.WithFields(logrus.Fields{
		"validator_id": validatorID,
		"reason":       event.Data["reason"],
	}).Info("Validator became inactive")
}

// handleBatchSubmitted processes validator batch submission
func (vt *ValidatorTracker) handleBatchSubmitted(ctx context.Context, event *Event) {
	validatorID := event.EntityID

	// Update last batch submission time
	vt.tracker.redis.HSet(ctx, fmt.Sprintf("validator:%s:stats", validatorID), "last_batch", event.Timestamp)

	// Increment batch counter
	vt.tracker.redis.HIncrBy(ctx, fmt.Sprintf("validator:%s:stats", validatorID), "batches_submitted", 1)

	// Link batch to validator if provided
	if batchID, ok := event.Data["batch_id"].(string); ok && batchID != "" {
		vt.tracker.redis.SAdd(ctx, fmt.Sprintf("validator:%s:batches", validatorID), batchID)
	}

	// Link to epoch if provided
	if epochID, ok := event.Data["epoch_id"].(string); ok && epochID != "" {
		vt.tracker.redis.SAdd(ctx, fmt.Sprintf("epoch:%s:validators", epochID), validatorID)
		vt.tracker.redis.SAdd(ctx, fmt.Sprintf("validator:%s:epochs", validatorID), epochID)
	}

	// Track submission count if provided
	if count, ok := event.Data["submissions_count"].(float64); ok {
		vt.tracker.redis.HIncrBy(ctx, fmt.Sprintf("validator:%s:stats", validatorID), "total_submissions", int64(count))
	}

	log.WithFields(logrus.Fields{
		"validator_id": validatorID,
		"batch_id":     event.Data["batch_id"],
		"epoch_id":     event.Data["epoch_id"],
	}).Debug("Validator submitted batch")
}

// UpdateIndexes updates validator-specific indexes during state transitions
func (vt *ValidatorTracker) UpdateIndexes(pipe redis.Pipeliner, validatorID, oldState, newState string, metadata map[string]interface{}) {
	ctx := context.Background()

	// Remove from old state set
	if oldState != "" {
		switch oldState {
		case ValidatorStateOffline:
			pipe.SRem(ctx, "offline:validators", validatorID)
		case ValidatorStateOnline:
			pipe.SRem(ctx, "online:validators", validatorID)
		case ValidatorStateActive:
			pipe.SRem(ctx, "active:validators", validatorID)
		case ValidatorStateInactive:
			pipe.SRem(ctx, "inactive:validators", validatorID)
		}
	}

	// Add to new state set
	switch newState {
	case ValidatorStateOffline:
		pipe.SAdd(ctx, "offline:validators", validatorID)
	case ValidatorStateOnline:
		pipe.SAdd(ctx, "online:validators", validatorID)
	case ValidatorStateActive:
		pipe.SAdd(ctx, "active:validators", validatorID)
	case ValidatorStateInactive:
		pipe.SAdd(ctx, "inactive:validators", validatorID)
	}

	// Update validator-specific indexes
	if peerID, ok := metadata["peer_id"]; ok {
		if peer, ok := peerID.(string); ok && peer != "" {
			pipe.HSet(ctx, "validators:peer_mapping", peer, validatorID)
		}
	}

	if stakeAmount, ok := metadata["stake_amount"]; ok {
		if stake, ok := stakeAmount.(float64); ok {
			pipe.ZAdd(ctx, "validators:by_stake", &redis.Z{
				Score:  stake,
				Member: validatorID,
			})
		}
	}
}

// GetValidatorStats retrieves statistics for a validator
func (vt *ValidatorTracker) GetValidatorStats(ctx context.Context, validatorID string) (map[string]interface{}, error) {
	// Get basic state
	state, err := vt.tracker.GetState(ctx, "validator", validatorID)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	for k, v := range state {
		// Convert numeric strings to proper types
		if k == "online_at" || k == "offline_at" || k == "active_at" || k == "inactive_at" || k == "updated_at" {
			if val, err := strconv.ParseInt(v, 10, 64); err == nil {
				stats[k] = val
			} else {
				stats[k] = v
			}
		} else {
			stats[k] = v
		}
	}

	// Get additional stats from hash
	hashStats, _ := vt.tracker.redis.HGetAll(ctx, fmt.Sprintf("validator:%s:stats", validatorID)).Result()
	for k, v := range hashStats {
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			stats[k] = val
		} else {
			stats[k] = v
		}
	}

	// Get epoch participation count
	epochs, _ := vt.tracker.redis.SCard(ctx, fmt.Sprintf("validator:%s:epochs", validatorID)).Result()
	stats["epochs_count"] = epochs

	// Get batch submission count
	batches, _ := vt.tracker.redis.SCard(ctx, fmt.Sprintf("validator:%s:batches", validatorID)).Result()
	stats["batches_count"] = batches

	// Get last seen time
	lastSeen, _ := vt.tracker.redis.HGet(ctx, "validators:last_seen", validatorID).Result()
	if lastSeen != "" {
		if val, err := strconv.ParseInt(lastSeen, 10, 64); err == nil {
			stats["last_seen"] = val
			// Calculate time since last seen
			stats["seconds_since_seen"] = time.Now().Unix() - val
		}
	}

	return stats, nil
}

// GetActiveValidators returns list of currently active validators
func (vt *ValidatorTracker) GetActiveValidators(ctx context.Context) ([]map[string]interface{}, error) {
	validatorIDs, err := vt.tracker.redis.SMembers(ctx, "active:validators").Result()
	if err != nil {
		return nil, err
	}

	validators := make([]map[string]interface{}, 0, len(validatorIDs))
	for _, validatorID := range validatorIDs {
		stats, err := vt.GetValidatorStats(ctx, validatorID)
		if err != nil {
			continue
		}
		stats["validator_id"] = validatorID
		validators = append(validators, stats)
	}

	return validators, nil
}

// GetValidatorsByStake returns validators sorted by stake amount
func (vt *ValidatorTracker) GetValidatorsByStake(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 10
	}

	// Get top validators by stake
	validators, err := vt.tracker.redis.ZRevRangeWithScores(ctx, "validators:by_stake", 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(validators))
	for _, z := range validators {
		validatorID := z.Member.(string)
		stake := z.Score

		stats, err := vt.GetValidatorStats(ctx, validatorID)
		if err != nil {
			continue
		}
		stats["validator_id"] = validatorID
		stats["stake_amount"] = stake
		result = append(result, stats)
	}

	return result, nil
}

// GetValidatorEpochs returns epochs a validator participated in
func (vt *ValidatorTracker) GetValidatorEpochs(ctx context.Context, validatorID string) ([]string, error) {
	return vt.tracker.redis.SMembers(ctx, fmt.Sprintf("validator:%s:epochs", validatorID)).Result()
}

// GetValidatorBatches returns batches submitted by a validator
func (vt *ValidatorTracker) GetValidatorBatches(ctx context.Context, validatorID string) ([]string, error) {
	return vt.tracker.redis.SMembers(ctx, fmt.Sprintf("validator:%s:batches", validatorID)).Result()
}