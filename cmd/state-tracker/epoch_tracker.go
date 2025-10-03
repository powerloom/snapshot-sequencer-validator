package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

// EpochTracker manages epoch-specific state tracking
type EpochTracker struct {
	tracker *StateTracker
}

// NewEpochTracker creates a new epoch tracker
func NewEpochTracker(tracker *StateTracker) *EpochTracker {
	return &EpochTracker{tracker: tracker}
}

// ProcessEvent handles epoch-related events
func (et *EpochTracker) ProcessEvent(ctx context.Context, event *Event) {
	switch event.Type {
	case "epoch_created":
		et.handleEpochCreated(ctx, event)
	case "epoch_started":
		et.handleEpochStarted(ctx, event)
	case "epoch_window_closed":
		et.handleWindowClosed(ctx, event)
	case "epoch_finalized":
		et.handleEpochFinalized(ctx, event)
	case "epoch_aggregated":
		et.handleEpochAggregated(ctx, event)
	}
}

// handleEpochCreated processes new epoch creation
func (et *EpochTracker) handleEpochCreated(ctx context.Context, event *Event) {
	epochID := event.EntityID
	metadata := map[string]interface{}{
		"created_at":   event.Timestamp,
		"block_height": event.Data["block_height"],
		"chain_id":     event.Data["chain_id"],
	}

	// Update state to pending
	if err := et.tracker.UpdateState(ctx, "epoch", epochID, EpochStatePending, metadata); err != nil {
		log.WithError(err).WithField("epoch_id", epochID).Error("Failed to update epoch state")
		return
	}

	// Add to pending epochs set
	et.tracker.redis.SAdd(ctx, "pending:epochs", epochID)

	// Add to time index
	et.tracker.redis.ZAdd(ctx, "metrics:epochs:by_time", &redis.Z{
		Score:  float64(event.Timestamp),
		Member: epochID,
	})

	log.WithField("epoch_id", epochID).Info("Epoch created and tracking started")
}

// handleEpochStarted processes epoch start event
func (et *EpochTracker) handleEpochStarted(ctx context.Context, event *Event) {
	epochID := event.EntityID
	metadata := map[string]interface{}{
		"started_at":      event.Timestamp,
		"submission_window": event.Data["window_duration"],
	}

	// Update state to processing
	if err := et.tracker.UpdateState(ctx, "epoch", epochID, EpochStateProcessing, metadata); err != nil {
		log.WithError(err).WithField("epoch_id", epochID).Error("Failed to update epoch state")
		return
	}

	// Move from pending to active
	pipe := et.tracker.redis.Pipeline()
	pipe.SRem(ctx, "pending:epochs", epochID)
	pipe.SAdd(ctx, "active:epochs", epochID)
	pipe.Exec(ctx)

	// Update active epochs gauge
	activeCount, _ := et.tracker.redis.SCard(ctx, "active:epochs").Result()
	activeEntities.WithLabelValues("epoch").Set(float64(activeCount))

	log.WithField("epoch_id", epochID).Info("Epoch started processing")
}

// handleWindowClosed processes submission window closure
func (et *EpochTracker) handleWindowClosed(ctx context.Context, event *Event) {
	epochID := event.EntityID
	metadata := map[string]interface{}{
		"window_closed_at": event.Timestamp,
		"submissions_count": event.Data["submissions_count"],
	}

	// Update state to finalizing
	if err := et.tracker.UpdateState(ctx, "epoch", epochID, EpochStateFinalizing, metadata); err != nil {
		log.WithError(err).WithField("epoch_id", epochID).Error("Failed to update epoch state")
		return
	}

	// Track submission count if provided
	if count, ok := event.Data["submissions_count"].(float64); ok {
		et.tracker.redis.HSet(ctx, fmt.Sprintf("epoch:%s:stats", epochID), "submissions", int(count))
	}

	log.WithField("epoch_id", epochID).Info("Epoch submission window closed")
}

// handleEpochFinalized processes epoch finalization
func (et *EpochTracker) handleEpochFinalized(ctx context.Context, event *Event) {
	epochID := event.EntityID
	metadata := map[string]interface{}{
		"finalized_at": event.Timestamp,
		"batch_cid":    event.Data["batch_cid"],
		"merkle_root":  event.Data["merkle_root"],
	}

	// Update state to aggregating
	if err := et.tracker.UpdateState(ctx, "epoch", epochID, EpochStateAggregating, metadata); err != nil {
		log.WithError(err).WithField("epoch_id", epochID).Error("Failed to update epoch state")
		return
	}

	// Add to finalization time index
	et.tracker.redis.ZAdd(ctx, "metrics:finalizations:by_time", &redis.Z{
		Score:  float64(event.Timestamp),
		Member: fmt.Sprintf("%s:%s", epochID, event.Data["batch_cid"]),
	})

	log.WithField("epoch_id", epochID).Info("Epoch finalized, awaiting aggregation")
}

// handleEpochAggregated processes epoch aggregation completion
func (et *EpochTracker) handleEpochAggregated(ctx context.Context, event *Event) {
	epochID := event.EntityID
	metadata := map[string]interface{}{
		"completed_at":     event.Timestamp,
		"validators_count": event.Data["validators_count"],
		"consensus_cid":    event.Data["consensus_cid"],
	}

	// Update state to completed
	if err := et.tracker.UpdateState(ctx, "epoch", epochID, EpochStateCompleted, metadata); err != nil {
		log.WithError(err).WithField("epoch_id", epochID).Error("Failed to update epoch state")
		return
	}

	// Move from active to completed
	pipe := et.tracker.redis.Pipeline()
	pipe.SRem(ctx, "active:epochs", epochID)
	pipe.SAdd(ctx, "completed:epochs", epochID)

	// Set expiry for completed epoch (7 days)
	stateKey := fmt.Sprintf("state:epoch:%s", epochID)
	pipe.Expire(ctx, stateKey, 7*24*time.Hour)

	pipe.Exec(ctx)

	// Update active epochs gauge
	activeCount, _ := et.tracker.redis.SCard(ctx, "active:epochs").Result()
	activeEntities.WithLabelValues("epoch").Set(float64(activeCount))

	log.WithFields(logrus.Fields{
		"epoch_id":    epochID,
		"validators":  event.Data["validators_count"],
		"consensus":   event.Data["consensus_cid"],
	}).Info("Epoch completed and aggregated")
}

// UpdateIndexes updates epoch-specific indexes during state transitions
func (et *EpochTracker) UpdateIndexes(pipe redis.Pipeliner, epochID, oldState, newState string, metadata map[string]interface{}) {
	ctx := context.Background()

	// Remove from old state set
	if oldState != "" {
		switch oldState {
		case EpochStatePending:
			pipe.SRem(ctx, "pending:epochs", epochID)
		case EpochStateProcessing, EpochStateFinalizing, EpochStateAggregating:
			pipe.SRem(ctx, "active:epochs", epochID)
		case EpochStateCompleted:
			pipe.SRem(ctx, "completed:epochs", epochID)
		}
	}

	// Add to new state set
	switch newState {
	case EpochStatePending:
		pipe.SAdd(ctx, "pending:epochs", epochID)
	case EpochStateProcessing, EpochStateFinalizing, EpochStateAggregating:
		pipe.SAdd(ctx, "active:epochs", epochID)
	case EpochStateCompleted:
		pipe.SAdd(ctx, "completed:epochs", epochID)
	}

	// Update epoch-specific indexes
	if submissions, ok := metadata["submissions"]; ok {
		if subList, ok := submissions.([]string); ok {
			for _, subID := range subList {
				pipe.SAdd(ctx, fmt.Sprintf("epoch:%s:submissions", epochID), subID)
			}
		}
	}

	if validators, ok := metadata["validators"]; ok {
		if valList, ok := validators.([]string); ok {
			for _, valID := range valList {
				pipe.SAdd(ctx, fmt.Sprintf("epoch:%s:validators", epochID), valID)
			}
		}
	}
}

// GetEpochStats retrieves statistics for an epoch
func (et *EpochTracker) GetEpochStats(ctx context.Context, epochID string) (map[string]interface{}, error) {
	// Get basic state
	state, err := et.tracker.GetState(ctx, "epoch", epochID)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	for k, v := range state {
		stats[k] = v
	}

	// Get submission count
	submissions, _ := et.tracker.redis.SCard(ctx, fmt.Sprintf("epoch:%s:submissions", epochID)).Result()
	stats["submissions_count"] = submissions

	// Get validator count
	validators, _ := et.tracker.redis.SCard(ctx, fmt.Sprintf("epoch:%s:validators", epochID)).Result()
	stats["validators_count"] = validators

	// Get additional stats from hash
	hashStats, _ := et.tracker.redis.HGetAll(ctx, fmt.Sprintf("epoch:%s:stats", epochID)).Result()
	for k, v := range hashStats {
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			stats[k] = val
		} else {
			stats[k] = v
		}
	}

	return stats, nil
}

// GetActiveEpochs returns list of currently active epochs
func (et *EpochTracker) GetActiveEpochs(ctx context.Context) ([]map[string]interface{}, error) {
	epochIDs, err := et.tracker.redis.SMembers(ctx, "active:epochs").Result()
	if err != nil {
		return nil, err
	}

	epochs := make([]map[string]interface{}, 0, len(epochIDs))
	for _, epochID := range epochIDs {
		stats, err := et.GetEpochStats(ctx, epochID)
		if err != nil {
			continue
		}
		stats["epoch_id"] = epochID
		epochs = append(epochs, stats)
	}

	return epochs, nil
}