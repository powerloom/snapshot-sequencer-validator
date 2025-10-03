package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

// SubmissionTracker manages submission-specific state tracking
type SubmissionTracker struct {
	tracker *StateTracker
}

// NewSubmissionTracker creates a new submission tracker
func NewSubmissionTracker(tracker *StateTracker) *SubmissionTracker {
	return &SubmissionTracker{tracker: tracker}
}

// ProcessEvent handles submission-related events
func (st *SubmissionTracker) ProcessEvent(ctx context.Context, event *Event) {
	switch event.Type {
	case "submission_received":
		st.handleSubmissionReceived(ctx, event)
	case "submission_validated":
		st.handleSubmissionValidated(ctx, event)
	case "submission_rejected":
		st.handleSubmissionRejected(ctx, event)
	case "submission_finalized":
		st.handleSubmissionFinalized(ctx, event)
	}
}

// handleSubmissionReceived processes new submission receipt
func (st *SubmissionTracker) handleSubmissionReceived(ctx context.Context, event *Event) {
	submissionID := event.EntityID
	metadata := map[string]interface{}{
		"received_at":   event.Timestamp,
		"epoch_id":      event.Data["epoch_id"],
		"project_id":    event.Data["project_id"],
		"snapshotter":   event.Data["snapshotter"],
		"snapshot_cid":  event.Data["snapshot_cid"],
		"slot_id":       event.Data["slot_id"],
	}

	// Update state to received
	if err := st.tracker.UpdateState(ctx, "submission", submissionID, SubmissionStateReceived, metadata); err != nil {
		log.WithError(err).WithField("submission_id", submissionID).Error("Failed to update submission state")
		return
	}

	// Add to active submissions
	st.tracker.redis.SAdd(ctx, "active:submissions", submissionID)

	// Add to time index
	st.tracker.redis.ZAdd(ctx, "metrics:submissions:by_time", &redis.Z{
		Score:  float64(event.Timestamp),
		Member: submissionID,
	})

	// Link to epoch if provided
	if epochID, ok := event.Data["epoch_id"].(string); ok && epochID != "" {
		st.tracker.redis.SAdd(ctx, fmt.Sprintf("epoch:%s:submissions", epochID), submissionID)
	}

	// Track by snapshotter
	if snapshotter, ok := event.Data["snapshotter"].(string); ok && snapshotter != "" {
		st.tracker.redis.SAdd(ctx, fmt.Sprintf("snapshotter:%s:submissions", snapshotter), submissionID)
	}

	log.WithFields(logrus.Fields{
		"submission_id": submissionID,
		"epoch_id":      event.Data["epoch_id"],
		"project_id":    event.Data["project_id"],
	}).Debug("Submission received and tracking started")
}

// handleSubmissionValidated processes submission validation
func (st *SubmissionTracker) handleSubmissionValidated(ctx context.Context, event *Event) {
	submissionID := event.EntityID
	metadata := map[string]interface{}{
		"validated_at":    event.Timestamp,
		"validator":       event.Data["validator"],
		"signature_valid": event.Data["signature_valid"],
	}

	// Update state to validated
	if err := st.tracker.UpdateState(ctx, "submission", submissionID, SubmissionStateValidated, metadata); err != nil {
		log.WithError(err).WithField("submission_id", submissionID).Error("Failed to update submission state")
		return
	}

	// Track validator confirmation
	if validator, ok := event.Data["validator"].(string); ok && validator != "" {
		st.tracker.redis.SAdd(ctx, fmt.Sprintf("submission:%s:validators", submissionID), validator)
	}

	// Update validation stats
	st.tracker.redis.HIncrBy(ctx, "stats:validations", "total", 1)
	st.tracker.redis.HIncrBy(ctx, "stats:validations", "validated", 1)

	log.WithFields(logrus.Fields{
		"submission_id": submissionID,
		"validator":     event.Data["validator"],
	}).Debug("Submission validated")
}

// handleSubmissionRejected processes submission rejection
func (st *SubmissionTracker) handleSubmissionRejected(ctx context.Context, event *Event) {
	submissionID := event.EntityID
	metadata := map[string]interface{}{
		"rejected_at": event.Timestamp,
		"reason":      event.Data["reason"],
		"validator":   event.Data["validator"],
	}

	// Update state to rejected
	if err := st.tracker.UpdateState(ctx, "submission", submissionID, SubmissionStateRejected, metadata); err != nil {
		log.WithError(err).WithField("submission_id", submissionID).Error("Failed to update submission state")
		return
	}

	// Remove from active submissions
	st.tracker.redis.SRem(ctx, "active:submissions", submissionID)
	st.tracker.redis.SAdd(ctx, "rejected:submissions", submissionID)

	// Update rejection stats
	st.tracker.redis.HIncrBy(ctx, "stats:validations", "total", 1)
	st.tracker.redis.HIncrBy(ctx, "stats:validations", "rejected", 1)

	// Track rejection reason
	if reason, ok := event.Data["reason"].(string); ok && reason != "" {
		st.tracker.redis.HIncrBy(ctx, "stats:rejection_reasons", reason, 1)
	}

	log.WithFields(logrus.Fields{
		"submission_id": submissionID,
		"reason":        event.Data["reason"],
	}).Info("Submission rejected")
}

// handleSubmissionFinalized processes submission finalization
func (st *SubmissionTracker) handleSubmissionFinalized(ctx context.Context, event *Event) {
	submissionID := event.EntityID
	metadata := map[string]interface{}{
		"finalized_at":      event.Timestamp,
		"batch_cid":         event.Data["batch_cid"],
		"validators_count":  event.Data["validators_count"],
		"consensus_reached": event.Data["consensus_reached"],
	}

	// Update state to finalized
	if err := st.tracker.UpdateState(ctx, "submission", submissionID, SubmissionStateFinalized, metadata); err != nil {
		log.WithError(err).WithField("submission_id", submissionID).Error("Failed to update submission state")
		return
	}

	// Move from active to finalized
	pipe := st.tracker.redis.Pipeline()
	pipe.SRem(ctx, "active:submissions", submissionID)
	pipe.SAdd(ctx, "finalized:submissions", submissionID)

	// Set expiry for finalized submission (3 days)
	stateKey := fmt.Sprintf("state:submission:%s", submissionID)
	pipe.Expire(ctx, stateKey, 3*24*time.Hour)

	pipe.Exec(ctx)

	// Update active submissions gauge
	activeCount, _ := st.tracker.redis.SCard(ctx, "active:submissions").Result()
	activeEntities.WithLabelValues("submission").Set(float64(activeCount))

	log.WithFields(logrus.Fields{
		"submission_id": submissionID,
		"batch_cid":     event.Data["batch_cid"],
	}).Debug("Submission finalized")
}

// UpdateIndexes updates submission-specific indexes during state transitions
func (st *SubmissionTracker) UpdateIndexes(pipe redis.Pipeliner, submissionID, oldState, newState string, metadata map[string]interface{}) {
	ctx := context.Background()

	// Remove from old state set
	if oldState != "" {
		switch oldState {
		case SubmissionStateReceived, SubmissionStateValidating, SubmissionStateValidated:
			pipe.SRem(ctx, "active:submissions", submissionID)
		case SubmissionStateRejected:
			pipe.SRem(ctx, "rejected:submissions", submissionID)
		case SubmissionStateFinalized:
			pipe.SRem(ctx, "finalized:submissions", submissionID)
		}
	}

	// Add to new state set
	switch newState {
	case SubmissionStateReceived, SubmissionStateValidating, SubmissionStateValidated:
		pipe.SAdd(ctx, "active:submissions", submissionID)
	case SubmissionStateRejected:
		pipe.SAdd(ctx, "rejected:submissions", submissionID)
	case SubmissionStateFinalized:
		pipe.SAdd(ctx, "finalized:submissions", submissionID)
	}

	// Update submission-specific indexes
	if epochID, ok := metadata["epoch_id"]; ok {
		if epoch, ok := epochID.(string); ok && epoch != "" {
			pipe.SAdd(ctx, fmt.Sprintf("epoch:%s:submissions", epoch), submissionID)
			pipe.SAdd(ctx, fmt.Sprintf("submission:%s:epochs", submissionID), epoch)
		}
	}

	if projectID, ok := metadata["project_id"]; ok {
		if project, ok := projectID.(string); ok && project != "" {
			pipe.SAdd(ctx, fmt.Sprintf("project:%s:submissions", project), submissionID)
		}
	}

	if snapshotter, ok := metadata["snapshotter"]; ok {
		if snap, ok := snapshotter.(string); ok && snap != "" {
			pipe.SAdd(ctx, fmt.Sprintf("snapshotter:%s:submissions", snap), submissionID)
		}
	}
}

// GetSubmissionStats retrieves statistics for a submission
func (st *SubmissionTracker) GetSubmissionStats(ctx context.Context, submissionID string) (map[string]interface{}, error) {
	// Get basic state
	state, err := st.tracker.GetState(ctx, "submission", submissionID)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	for k, v := range state {
		// Convert numeric strings to proper types
		if k == "received_at" || k == "validated_at" || k == "rejected_at" || k == "finalized_at" || k == "updated_at" {
			if val, err := strconv.ParseInt(v, 10, 64); err == nil {
				stats[k] = val
			} else {
				stats[k] = v
			}
		} else {
			stats[k] = v
		}
	}

	// Get validator confirmations
	validators, _ := st.tracker.redis.SMembers(ctx, fmt.Sprintf("submission:%s:validators", submissionID)).Result()
	stats["validators_confirming"] = validators
	stats["validators_count"] = len(validators)

	// Get linked epochs
	epochs, _ := st.tracker.redis.SMembers(ctx, fmt.Sprintf("submission:%s:epochs", submissionID)).Result()
	if len(epochs) > 0 {
		stats["epochs"] = epochs
	}

	return stats, nil
}

// GetActiveSubmissions returns list of currently active submissions
func (st *SubmissionTracker) GetActiveSubmissions(ctx context.Context) ([]map[string]interface{}, error) {
	submissionIDs, err := st.tracker.redis.SMembers(ctx, "active:submissions").Result()
	if err != nil {
		return nil, err
	}

	submissions := make([]map[string]interface{}, 0, len(submissionIDs))
	for _, submissionID := range submissionIDs {
		stats, err := st.GetSubmissionStats(ctx, submissionID)
		if err != nil {
			continue
		}
		stats["submission_id"] = submissionID
		submissions = append(submissions, stats)
	}

	return submissions, nil
}

// GetSubmissionsByProject retrieves all submissions for a project
func (st *SubmissionTracker) GetSubmissionsByProject(ctx context.Context, projectID string) ([]string, error) {
	return st.tracker.redis.SMembers(ctx, fmt.Sprintf("project:%s:submissions", projectID)).Result()
}

// GetSubmissionsBySnapshotter retrieves all submissions from a snapshotter
func (st *SubmissionTracker) GetSubmissionsBySnapshotter(ctx context.Context, snapshotter string) ([]string, error) {
	return st.tracker.redis.SMembers(ctx, fmt.Sprintf("snapshotter:%s:submissions", snapshotter)).Result()
}