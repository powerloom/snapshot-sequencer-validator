package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	redislib "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/rpcmetrics"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// State constants
const (
	// Epoch states
	EpochStatePending     = "pending"
	EpochStateProcessing  = "processing"
	EpochStateFinalizing  = "finalizing"
	EpochStateAggregating = "aggregating"
	EpochStateCompleted   = "completed"

	// Submission states
	SubmissionStateReceived   = "received"
	SubmissionStateValidating = "validating"
	SubmissionStateValidated  = "validated"
	SubmissionStateRejected   = "rejected"
	SubmissionStateFinalized  = "finalized"

	// Validator states
	ValidatorStateOffline  = "offline"
	ValidatorStateOnline   = "online"
	ValidatorStateActive   = "active"
	ValidatorStateInactive = "inactive"
)

// Event represents a state change event
type Event struct {
	Type      string                 `json:"type"`
	EntityID  string                 `json:"entity_id"`
	Timestamp int64                  `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// StateTracker manages state transitions and indexes
type StateTracker struct {
	redis         *redis.Client
	pubsub        *redis.PubSub
	metricsClient *rpcmetrics.MetricsClient
	keyBuilder    *redislib.KeyBuilder

	// Sub-trackers
	epochTracker      *EpochTracker
	submissionTracker *SubmissionTracker
	validatorTracker  *ValidatorTracker

	// Control
	mu       sync.RWMutex
	shutdown chan struct{}
	wg       sync.WaitGroup
}

// NewStateTracker creates a new state tracker instance
func NewStateTracker(redisClient *redis.Client, metricsClient *rpcmetrics.MetricsClient) *StateTracker {
	protocol := viper.GetString("protocol")
	market := viper.GetString("market")
	keyBuilder := redislib.NewKeyBuilder(protocol, market)

	tracker := &StateTracker{
		redis:         redisClient,
		metricsClient: metricsClient,
		keyBuilder:    keyBuilder,
		shutdown:      make(chan struct{}),
	}

	// Initialize sub-trackers
	tracker.epochTracker = NewEpochTracker(tracker)
	tracker.submissionTracker = NewSubmissionTracker(tracker)
	tracker.validatorTracker = NewValidatorTracker(tracker)

	return tracker
}

// StartEventListener subscribes to event channels and processes state changes
func (st *StateTracker) StartEventListener(ctx context.Context) {
	st.wg.Add(1)
	defer st.wg.Done()

	// Subscribe to event channels
	channels := []string{
		"events:epoch:*",
		"events:submission:*",
		"events:validator:*",
		"events:finalization:*",
		"events:aggregation:*",
	}

	st.pubsub = st.redis.PSubscribe(ctx, channels...)
	defer st.pubsub.Close()

	log.WithField("channels", channels).Info("State Tracker listening for events")

	ch := st.pubsub.Channel()
	for {
		select {
		case <-st.shutdown:
			return
		case msg := <-ch:
			if msg == nil {
				continue
			}

			// Parse event
			var event Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.WithError(err).Error("Failed to parse event")
				continue
			}

			// Process event
			st.processEvent(ctx, &event)
		}
	}
}

// processEvent routes events to appropriate handlers
func (st *StateTracker) processEvent(ctx context.Context, event *Event) {
	log.WithFields(logrus.Fields{
		"type":      event.Type,
		"entity_id": event.EntityID,
		"timestamp": event.Timestamp,
	}).Debug("Processing event")

	switch event.Type {
	case "epoch_created", "epoch_started", "epoch_window_closed", "epoch_finalized", "epoch_aggregated":
		st.epochTracker.ProcessEvent(ctx, event)

	case "submission_received", "submission_validated", "submission_rejected", "submission_finalized":
		st.submissionTracker.ProcessEvent(ctx, event)

	case "validator_online", "validator_offline", "validator_active", "validator_inactive", "validator_batch_submitted":
		st.validatorTracker.ProcessEvent(ctx, event)

	default:
		log.WithField("type", event.Type).Warn("Unknown event type")
	}

	// Send metrics if client is configured
	if st.metricsClient != nil {
		st.sendEventMetric(event)
	}
}

// sendEventMetric sends event data to metrics service
func (st *StateTracker) sendEventMetric(event *Event) {
	metric := &rpcmetrics.EventMetric{
		EventType: event.Type,
		EntityID:  event.EntityID,
		Timestamp: event.Timestamp,
		Metadata:  event.Data,
	}

	if err := st.metricsClient.RecordEvent(context.Background(), metric); err != nil {
		log.WithError(err).Debug("Failed to send event metric")
	}
}

// UpdateState atomically updates entity state
func (st *StateTracker) UpdateState(ctx context.Context, entityType, entityID, newState string, metadata map[string]interface{}) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := fmt.Sprintf("state:%s:%s", entityType, entityID)

	// Get current state
	currentState, _ := st.redis.HGet(ctx, key, "state").Result()

	// Prepare update
	updates := map[string]interface{}{
		"state":      newState,
		"updated_at": time.Now().Unix(),
	}

	// Add metadata fields
	for k, v := range metadata {
		updates[k] = v
	}

	// Update state
	pipe := st.redis.Pipeline()
	pipe.HMSet(ctx, key, updates)

	// Update indexes based on entity type
	switch entityType {
	case "epoch":
		st.epochTracker.UpdateIndexes(pipe, entityID, currentState, newState, metadata)
	case "submission":
		st.submissionTracker.UpdateIndexes(pipe, entityID, currentState, newState, metadata)
	case "validator":
		st.validatorTracker.UpdateIndexes(pipe, entityID, currentState, newState, metadata)
	}

	// Execute pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	// Update metrics
	stateTransitions.WithLabelValues(entityType, currentState, newState).Inc()

	log.WithFields(logrus.Fields{
		"entity_type": entityType,
		"entity_id":   entityID,
		"from_state":  currentState,
		"to_state":    newState,
	}).Debug("State updated")

	return nil
}

// GetState retrieves current state for an entity
func (st *StateTracker) GetState(ctx context.Context, entityType, entityID string) (map[string]string, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := fmt.Sprintf("state:%s:%s", entityType, entityID)
	result, err := st.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("entity not found: %s:%s", entityType, entityID)
	}

	return result, nil
}

// GetActiveEntities returns IDs of active entities by type
func (st *StateTracker) GetActiveEntities(ctx context.Context, entityType string) ([]string, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var setKey string
	switch entityType {
	case "epoch":
		setKey = "active:epochs"
	case "submission":
		setKey = "active:submissions"
	case "validator":
		setKey = "active:validators"
	default:
		return nil, fmt.Errorf("unknown entity type: %s", entityType)
	}

	members, err := st.redis.SMembers(ctx, setKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get active entities: %w", err)
	}

	return members, nil
}

// GetTimeline retrieves state transitions within a time range
func (st *StateTracker) GetTimeline(ctx context.Context, entityType string, start, end int64) ([]map[string]interface{}, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var zsetKey string
	switch entityType {
	case "epoch":
		zsetKey = "metrics:epochs:by_time"
	case "submission":
		zsetKey = "metrics:submissions:by_time"
	case "validator":
		zsetKey = "metrics:validators:by_time"
	default:
		return nil, fmt.Errorf("unknown entity type: %s", entityType)
	}

	// Get entity IDs within time range
	entities, err := st.redis.ZRangeByScoreWithScores(ctx, zsetKey, &redis.ZRangeBy{
		Min: strconv.FormatInt(start, 10),
		Max: strconv.FormatInt(end, 10),
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	// Build timeline entries
	timeline := make([]map[string]interface{}, 0, len(entities))
	for _, z := range entities {
		entityID := z.Member.(string)
		timestamp := int64(z.Score)

		// Get entity state
		state, err := st.GetState(ctx, entityType, entityID)
		if err != nil {
			continue
		}

		entry := map[string]interface{}{
			"entity_id": entityID,
			"timestamp": timestamp,
			"state":     state["state"],
		}

		// Add relevant metadata
		for k, v := range state {
			if k != "state" && k != "updated_at" {
				entry[k] = v
			}
		}

		timeline = append(timeline, entry)
	}

	return timeline, nil
}

// StartCleanup periodically removes old state data
func (st *StateTracker) StartCleanup(ctx context.Context) {
	st.wg.Add(1)
	defer st.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-st.shutdown:
			return
		case <-ticker.C:
			st.cleanupOldState(ctx)
		}
	}
}

// cleanupOldState removes state data older than retention period
func (st *StateTracker) cleanupOldState(ctx context.Context) {
	retentionDays := viper.GetInt("state_tracker.retention_days")
	if retentionDays == 0 {
		retentionDays = 7 // Default to 7 days
	}

	cutoff := time.Now().Unix() - int64(retentionDays*86400)

	// Clean up time-based sorted sets
	zsets := []string{
		"metrics:epochs:by_time",
		"metrics:submissions:by_time",
		"metrics:validators:by_time",
		"metrics:finalizations:by_time",
	}

	for _, zset := range zsets {
		removed, err := st.redis.ZRemRangeByScore(ctx, zset, "-inf", strconv.FormatInt(cutoff, 10)).Result()
		if err != nil {
			log.WithError(err).WithField("zset", zset).Error("Failed to cleanup old entries")
			continue
		}

		if removed > 0 {
			log.WithFields(logrus.Fields{
				"zset":    zset,
				"removed": removed,
			}).Info("Cleaned up old state entries")
		}
	}
}

// Shutdown gracefully stops the state tracker
func (st *StateTracker) Shutdown() {
	close(st.shutdown)
	st.wg.Wait()

	if st.pubsub != nil {
		st.pubsub.Close()
	}
}