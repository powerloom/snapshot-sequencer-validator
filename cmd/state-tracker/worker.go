package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	redislib "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// StateWorker is a background worker that prepares datasets for monitor-api
type StateWorker struct {
	redis      *redis.Client
	pubsub     *redis.PubSub
	keyBuilder *redislib.KeyBuilder

	// In-memory counters for current metrics
	mu              sync.RWMutex
	submissions     int64
	validations     int64
	epochs          int64
	batches         int64
	lastReset       time.Time

	// Control
	shutdown chan struct{}
	wg       sync.WaitGroup
}

// StateChangeEvent represents a state change from the pipeline
type StateChangeEvent struct {
	Type       string                 `json:"type"`       // submission, validation, epoch, batch
	EntityID   string                 `json:"entity_id"`  // ID of the entity
	Timestamp  int64                  `json:"timestamp"`  // Unix timestamp
	Attributes map[string]interface{} `json:"attributes"` // Additional data
}

// NewStateWorker creates a new state worker instance
func NewStateWorker(redisClient *redis.Client, keyBuilder *redislib.KeyBuilder) *StateWorker {

	return &StateWorker{
		redis:      redisClient,
		keyBuilder: keyBuilder,
		lastReset:  time.Now(),
		shutdown:   make(chan struct{}),
	}
}

// StartStateChangeListener subscribes to state changes and counts them
func (sw *StateWorker) StartStateChangeListener(ctx context.Context) {
	sw.wg.Add(1)
	defer sw.wg.Done()

	// Subscribe to state change channel
	sw.pubsub = sw.redis.Subscribe(ctx, "state:change")
	defer sw.pubsub.Close()

	log.Info("State worker listening for state changes on 'state:change' channel")

	ch := sw.pubsub.Channel()
	for {
		select {
		case <-sw.shutdown:
			return
		case msg := <-ch:
			if msg == nil {
				continue
			}

			// Parse simple event format: "type:action:id"
			// e.g., "submission:received:123", "epoch:open:456"
			parts := strings.Split(msg.Payload, ":")
			if len(parts) < 2 {
				log.WithField("payload", msg.Payload).Debug("Invalid state change format")
				continue
			}

			eventType := parts[0]
			action := ""
			if len(parts) > 1 {
				action = parts[1]
			}
			sw.processSimpleStateChange(eventType, action)
		}
	}
}

// processSimpleStateChange updates counters from simple event format
func (sw *StateWorker) processSimpleStateChange(eventType string, action string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	switch eventType {
	case "submission":
		sw.submissions++
		stateChangesProcessed.WithLabelValues("submission").Inc()
		log.WithField("type", eventType).Debug("Received state change event")
	case "validation":
		sw.validations++
		stateChangesProcessed.WithLabelValues("validation").Inc()
		log.WithField("type", eventType).Debug("Received state change event")
	case "epoch":
		// Only count epoch opens (ignore closes to avoid double-counting)
		if action == "open" {
			sw.epochs++
			stateChangesProcessed.WithLabelValues("epoch").Inc()
			log.WithField("type", eventType).WithField("action", action).Debug("Received state change event")
		}
	case "batch":
		sw.batches++
		stateChangesProcessed.WithLabelValues("batch").Inc()
		log.WithField("type", eventType).Debug("Received state change event")
	case "part":
		// Parts are tracked but don't have separate counter
		stateChangesProcessed.WithLabelValues("part").Inc()
		log.WithField("type", eventType).Debug("Received state change event")
	default:
		log.WithField("type", eventType).Debug("Unknown event type received")
	}
}

// processStateChange updates in-memory counters based on state change (legacy)
func (sw *StateWorker) processStateChange(event *StateChangeEvent) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	switch event.Type {
	case "submission":
		atomic.AddInt64(&sw.submissions, 1)
		stateChangesProcessed.WithLabelValues("submission").Inc()

	case "validation":
		atomic.AddInt64(&sw.validations, 1)
		stateChangesProcessed.WithLabelValues("validation").Inc()

	case "epoch":
		atomic.AddInt64(&sw.epochs, 1)
		stateChangesProcessed.WithLabelValues("epoch").Inc()

	case "batch":
		atomic.AddInt64(&sw.batches, 1)
		stateChangesProcessed.WithLabelValues("batch").Inc()
	}


	log.WithFields(logrus.Fields{
		"type":      event.Type,
		"entity_id": event.EntityID,
	}).Debug("Processed state change")
}

// StartMetricsAggregator aggregates metrics every 30 seconds
func (sw *StateWorker) StartMetricsAggregator(ctx context.Context) {
	sw.wg.Add(1)
	defer sw.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Info("Metrics aggregator started - updating every 30 seconds")

	// Initial aggregation
	sw.aggregateCurrentMetrics(ctx)

	for {
		select {
		case <-sw.shutdown:
			return
		case <-ticker.C:
			sw.aggregateCurrentMetrics(ctx)
		}
	}
}

// aggregateCurrentMetrics prepares the dashboard:summary dataset
func (sw *StateWorker) aggregateCurrentMetrics(ctx context.Context) {
	start := time.Now()
	defer func() {
		aggregationDuration.WithLabelValues("current_metrics").Observe(time.Since(start).Seconds())
	}()

	sw.mu.RLock()

	// Calculate rates from in-memory counters (for recent activity)
	duration := time.Since(sw.lastReset).Seconds()
	submissionRate := float64(sw.submissions) / duration
	epochRate := float64(sw.epochs) / duration
	batchRate := float64(sw.batches) / duration

	// Get actual submission counts from Redis (more accurate)
	// Count submissions in submission queue
	submissionQueueCount, _ := sw.redis.LLen(ctx, sw.keyBuilder.SubmissionQueue()).Result()

	// Count processed submissions by scanning processed submission keys
	recentProcessedSubmissions := int64(0)
	// Use SCAN to find processed submission keys (production-safe)
	iter := sw.redis.Scan(ctx, 0, fmt.Sprintf("%s:%s:processed:*", sw.keyBuilder.ProtocolState, sw.keyBuilder.DataMarket), 100).Iterator()
	for iter.Next(ctx) {
		// Each key represents a processed submission
		recentProcessedSubmissions++
	}

	// Combine in-memory and Redis counts for total submissions
	totalSubmissions := sw.submissions + recentProcessedSubmissions

	sw.mu.RUnlock()

	// Count active validators using two-level reference (no SCAN)
	// 1. Get recent batches from timeline
	// 2. Read validator lists from each batch
	activeValidators := make(map[string]bool)
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute).Unix()

	// Get recent batch epochs from timeline
	recentBatches, _ := sw.redis.ZRangeByScore(ctx, sw.keyBuilder.MetricsBatchesTimeline(), &redis.ZRangeBy{
		Min: strconv.FormatInt(fiveMinutesAgo, 10),
		Max: "+inf",
	}).Result()

	// For each recent batch, get its validator list
	for _, batchEntry := range recentBatches {
		// Entry format: "aggregated:{epoch}" or "local:{epoch}"
		parts := strings.Split(batchEntry, ":")
		if len(parts) < 2 {
			continue
		}
		batchType := parts[0]
		epochID := parts[1]

		// Only aggregated batches have validator lists (Level 2)
		if batchType != "aggregated" {
			continue
		}

		validatorsKey := sw.keyBuilder.MetricsBatchValidators(epochID)
		validatorList, err := sw.redis.Get(ctx, validatorsKey).Result()
		if err == nil && validatorList != "" {
			// Parse validator list (stored as JSON array)
			var validators []string
			if err := json.Unmarshal([]byte(validatorList), &validators); err == nil {
				for _, v := range validators {
					if v != "" {
						activeValidators[v] = true
					}
				}
			}
		}
	}

	activeCount := len(activeValidators)

	// Prepare summary data with combined metrics
	summary := map[string]interface{}{
		"submissions_total":     totalSubmissions,
		"submissions_queue":     submissionQueueCount,
		"processed_submissions": recentProcessedSubmissions,
		"epochs_total":          sw.epochs,
		"batches_total":         sw.batches,
		"submission_rate":       submissionRate,
		"epoch_rate":            epochRate,
		"batch_rate":            batchRate,
		"active_validators":     activeCount,
		"updated_at":            time.Now().Unix(),
		"measurement_duration":  duration,
	}

	// Get recent activity counts from timelines
	nowTS := time.Now().Unix()
	oneMinuteAgo := nowTS - 60

	// Count recent epochs from timeline (both open and close events)
	recentEpochs, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsEpochsTimeline(),
		strconv.FormatInt(oneMinuteAgo, 10),
		strconv.FormatInt(nowTS, 10)).Result()
	if err != nil {
		recentEpochs = 0
	}
	summary["epochs_1m"] = recentEpochs

	// Count recent batches from timeline (both local and aggregated)
	var recentBatchesCount int64
	recentBatchesCount, err = sw.redis.ZCount(ctx, sw.keyBuilder.MetricsBatchesTimeline(),
		strconv.FormatInt(oneMinuteAgo, 10),
		strconv.FormatInt(nowTS, 10)).Result()
	if err != nil {
		recentBatchesCount = 0
	}
	summary["batches_1m"] = recentBatchesCount

	// Store summary with TTL
	summaryJSON, _ := json.Marshal(summary)
	sw.redis.Set(ctx, sw.keyBuilder.DashboardSummary(), summaryJSON, 60*time.Second)

	// Also store as hash for easier access
	sw.redis.HMSet(ctx, sw.keyBuilder.StatsCurrent(), summary)
	sw.redis.Expire(ctx, sw.keyBuilder.StatsCurrent(), 60*time.Second)

	datasetsGenerated.WithLabelValues("dashboard_summary").Inc()

	log.WithFields(logrus.Fields{
		"submissions_total": totalSubmissions,
		"queue_submissions": submissionQueueCount,
		"epochs":           sw.epochs,
		"batches":          sw.batches,
		"validators":       activeCount,
		"epochs_1m":        recentEpochs,
		"batches_1m":       recentBatchesCount,
	}).Debug("Updated dashboard summary")

	// Aggregate participation metrics
	sw.aggregateParticipationMetrics(ctx)

	// Aggregate current epoch status
	sw.aggregateCurrentEpochStatus(ctx)
}

// StartHourlyStatsWorker prepares hourly statistics every 5 minutes
func (sw *StateWorker) StartHourlyStatsWorker(ctx context.Context) {
	sw.wg.Add(1)
	defer sw.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	log.Info("Hourly stats worker started - updating every 5 minutes")

	// Initial aggregation
	sw.aggregateHourlyStats(ctx)

	for {
		select {
		case <-sw.shutdown:
			return
		case <-ticker.C:
			sw.aggregateHourlyStats(ctx)
		}
	}
}

// aggregateHourlyStats prepares stats for the current and previous hours
func (sw *StateWorker) aggregateHourlyStats(ctx context.Context) {
	start := time.Now()
	defer func() {
		aggregationDuration.WithLabelValues("hourly_stats").Observe(time.Since(start).Seconds())
	}()

	now := time.Now()
	currentHour := now.Truncate(time.Hour)
	previousHour := currentHour.Add(-time.Hour)

	// Aggregate stats for current hour
	sw.aggregateHourPeriod(ctx, currentHour, now)

	// Aggregate stats for previous hour (complete hour)
	sw.aggregateHourPeriod(ctx, previousHour, currentHour)

	datasetsGenerated.WithLabelValues("hourly_stats").Inc()
}

// aggregateHourPeriod aggregates stats for a specific hour
func (sw *StateWorker) aggregateHourPeriod(ctx context.Context, hourStart, hourEnd time.Time) {
	startTS := hourStart.Unix()
	endTS := hourEnd.Unix()

	// Count events in this hour (submissions/validations not tracked per market)
	epochs, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsEpochsTimeline(),
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()
	if err != nil {
		epochs = 0
	}

	batches, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsBatchesTimeline(),
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()
	if err != nil {
		batches = 0
	}

	// Count submissions by scanning processed submission keys
	submissionCount := int64(0)
	iter := sw.redis.Scan(ctx, 0, fmt.Sprintf("%s:%s:processed:*", sw.keyBuilder.ProtocolState, sw.keyBuilder.DataMarket), 1000).Iterator()
	for iter.Next(ctx) {
		submissionCount++
	}

	// Count unique validators using two-level reference (no SCAN)
	uniqueValidatorsMap := make(map[string]bool)
	hourBatches, _ := sw.redis.ZRangeByScore(ctx, sw.keyBuilder.MetricsBatchesTimeline(), &redis.ZRangeBy{
		Min: strconv.FormatInt(startTS, 10),
		Max: strconv.FormatInt(endTS, 10),
	}).Result()

	for _, batchEntry := range hourBatches {
		parts := strings.Split(batchEntry, ":")
		if len(parts) < 2 {
			continue
		}
		batchType := parts[0]
		epochID := parts[1]

		// Only aggregated batches have validator lists (Level 2)
		if batchType != "aggregated" {
			continue
		}

		validatorsKey := sw.keyBuilder.MetricsBatchValidators(epochID)
		validatorList, err := sw.redis.Get(ctx, validatorsKey).Result()
		if err == nil && validatorList != "" {
			// Parse validator list (stored as JSON array)
			var validators []string
			if err := json.Unmarshal([]byte(validatorList), &validators); err == nil {
				for _, v := range validators {
					if v != "" {
						uniqueValidatorsMap[v] = true
					}
				}
			}
		}
	}
	uniqueValidators := len(uniqueValidatorsMap)

	hourStats := map[string]interface{}{
		"hour_start":        hourStart.Format(time.RFC3339),
		"hour_end":          hourEnd.Format(time.RFC3339),
		"epochs":            epochs,
		"batches":           batches,
		"submissions":       submissionCount,
		"unique_validators": uniqueValidators,
		"updated_at":        time.Now().Unix(),
	}

	// Store with hour as key
	hourKey := sw.keyBuilder.StatsHourly(hourStart.Unix())
	statsJSON, _ := json.Marshal(hourStats)
	sw.redis.Set(ctx, hourKey, statsJSON, 2*time.Hour)

	log.WithFields(logrus.Fields{
		"hour":        hourStart.Format("15:04"),
		"epochs":      epochs,
		"batches":     batches,
		"submissions": submissionCount,
		"validators":  uniqueValidators,
	}).Debug("Updated hourly stats")
}

// StartDailyStatsWorker prepares daily statistics every hour
func (sw *StateWorker) StartDailyStatsWorker(ctx context.Context) {
	sw.wg.Add(1)
	defer sw.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Info("Daily stats worker started - updating every hour")

	// Initial aggregation
	sw.aggregateDailyStats(ctx)

	for {
		select {
		case <-sw.shutdown:
			return
		case <-ticker.C:
			sw.aggregateDailyStats(ctx)
		}
	}
}

// aggregateDailyStats prepares stats for the last 24 hours
func (sw *StateWorker) aggregateDailyStats(ctx context.Context) {
	start := time.Now()
	defer func() {
		aggregationDuration.WithLabelValues("daily_stats").Observe(time.Since(start).Seconds())
	}()

	now := time.Now()
	dayAgo := now.Add(-24 * time.Hour)

	startTS := dayAgo.Unix()
	endTS := now.Unix()

	// Count events in last 24 hours
	epochs, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsEpochsTimeline(),
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()
	if err != nil {
		epochs = 0
	}

	batches, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsBatchesTimeline(),
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()
	if err != nil {
		batches = 0
	}

	// Count submissions by scanning processed submission keys
	submissionCount := int64(0)
	iter := sw.redis.Scan(ctx, 0, fmt.Sprintf("%s:%s:processed:*", sw.keyBuilder.ProtocolState, sw.keyBuilder.DataMarket), 5000).Iterator()
	for iter.Next(ctx) {
		submissionCount++
	}

	// Get hourly breakdown
	hourlyBreakdown := make([]map[string]interface{}, 0, 24)
	for i := 0; i < 24; i++ {
		hourStart := dayAgo.Add(time.Duration(i) * time.Hour)
		hourEnd := hourStart.Add(time.Hour)

		hourEpochs, _ := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsEpochsTimeline(),
			strconv.FormatInt(hourStart.Unix(), 10),
			strconv.FormatInt(hourEnd.Unix(), 10)).Result()

		hourlyBreakdown = append(hourlyBreakdown, map[string]interface{}{
			"hour":   hourStart.Format("15:04"),
			"epochs": hourEpochs,
		})
	}

	dailyStats := map[string]interface{}{
		"period_start":     dayAgo.Format(time.RFC3339),
		"period_end":       now.Format(time.RFC3339),
		"epochs_total":     epochs,
		"batches_total":    batches,
		"submissions_total": submissionCount,
		"hourly_breakdown": hourlyBreakdown,
		"updated_at":       time.Now().Unix(),
	}

	// Store daily stats
	statsJSON, _ := json.Marshal(dailyStats)
	sw.redis.Set(ctx, sw.keyBuilder.StatsDaily(), statsJSON, 24*time.Hour)

	datasetsGenerated.WithLabelValues("daily_stats").Inc()

	log.WithFields(logrus.Fields{
		"epochs":      epochs,
		"batches":     batches,
		"submissions": submissionCount,
	}).Info("Updated daily stats")
}

// StartPruningWorker removes old data from sorted sets every hour
func (sw *StateWorker) StartPruningWorker(ctx context.Context) {
	sw.wg.Add(1)
	defer sw.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Info("Pruning worker started - cleaning old data every hour")

	for {
		select {
		case <-sw.shutdown:
			return
		case <-ticker.C:
			sw.pruneOldData(ctx)
		}
	}
}

// pruneOldData removes entries older than 24 hours from timeline sorted sets
func (sw *StateWorker) pruneOldData(ctx context.Context) {
	cutoff := time.Now().Add(-24 * time.Hour).Unix()

	// Only prune main timeline sorted sets (no per-validator keys needed)
	timelines := []string{
		sw.keyBuilder.MetricsEpochsTimeline(),
		sw.keyBuilder.MetricsBatchesTimeline(),
	}

	totalRemoved := int64(0)
	for _, timeline := range timelines {
		removed, err := sw.redis.ZRemRangeByScore(ctx, timeline,
			"-inf",
			strconv.FormatInt(cutoff, 10)).Result()
		if err != nil {
			log.WithError(err).WithField("timeline", timeline).Error("Failed to prune old data")
			continue
		}
		totalRemoved += removed
	}

	if totalRemoved > 0 {
		log.WithField("removed_entries", totalRemoved).Info("Pruned old timeline data")
	}

	// Also reset in-memory counters if they've been running for more than 24 hours
	sw.mu.Lock()
	if time.Since(sw.lastReset) > 24*time.Hour {
		sw.submissions = 0
		sw.validations = 0
		sw.epochs = 0
		sw.batches = 0
		sw.lastReset = time.Now()

		log.Info("Reset in-memory counters after 24 hours")
	}
	sw.mu.Unlock()
}

// aggregateParticipationMetrics calculates participation and inclusion rates
func (sw *StateWorker) aggregateParticipationMetrics(ctx context.Context) {
	now := time.Now().Unix()
	last24h := now - 86400

	// Get sequencer ID - must match what aggregator uses
	// This is critical for accurate participation metrics
	sequencerID := viper.GetString("sequencer_id")
	if sequencerID == "" {
		// Try to get from environment as fallback
		sequencerID = os.Getenv("SEQUENCER_ID")
		if sequencerID == "" {
			sequencerID = "unified-sequencer-1" // final fallback
		}
	}

	// Count Level 1 batches (our local finalizations) in last 24h
	ourBatchesKey := sw.keyBuilder.MetricsValidatorBatches(sequencerID)
	level1Batches, err := sw.redis.ZCount(ctx, ourBatchesKey,
		strconv.FormatInt(last24h, 10),
		strconv.FormatInt(now, 10)).Result()
	if err != nil {
		log.WithError(err).WithField("validator_id", sequencerID).Debug("Failed to count validator batches")
		level1Batches = 0
	}

	// Count total Level 2 batches (network aggregations) in last 24h
	allLevel2Batches, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsBatchesTimeline(),
		strconv.FormatInt(last24h, 10),
		strconv.FormatInt(now, 10)).Result()
	if err != nil {
		log.WithError(err).Debug("Failed to count aggregated batches")
		allLevel2Batches = 0
	}

	// Count Level 2 batches where we were included
	level2Inclusions := int64(0)

	// Get recent aggregated batches from timeline
	recentAggregated, err := sw.redis.ZRangeByScore(ctx, sw.keyBuilder.MetricsBatchesTimeline(), &redis.ZRangeBy{
		Min: strconv.FormatInt(last24h, 10),
		Max: strconv.FormatInt(now, 10),
	}).Result()
	if err != nil {
		log.WithError(err).Debug("Failed to get recent aggregated batches")
		recentAggregated = []string{}
	}

	// For each aggregated batch, check if we contributed
	for _, entry := range recentAggregated {
		// Entry format: "aggregated:{epoch}" or "local:{epoch}"
		if !strings.HasPrefix(entry, "aggregated:") {
			continue
		}

		epochID := strings.TrimPrefix(entry, "aggregated:")

		// Check if our validator is in the validators list
		validatorsKey := sw.keyBuilder.MetricsBatchValidators(epochID)
		validatorsJSON, err := sw.redis.Get(ctx, validatorsKey).Result()
		if err != nil {
			// Validator list might not exist yet, skip
			continue
		}

		var validators []string
		if json.Unmarshal([]byte(validatorsJSON), &validators) == nil {
			for _, v := range validators {
				if v == sequencerID {
					level2Inclusions++
					break
				}
			}
		}
	}

	// Calculate metrics
	participationRate := 0.0
	if allLevel2Batches > 0 {
		participationRate = float64(level2Inclusions) / float64(allLevel2Batches) * 100
	}

	inclusionRate := 0.0
	if level1Batches > 0 {
		inclusionRate = float64(level2Inclusions) / float64(level1Batches) * 100
		if inclusionRate > 100 {
			inclusionRate = 100 // Cap at 100%
		}
	}

	// Count epochs in last 24h (both open and close events)
	epochsTotal, err := sw.redis.ZCount(ctx, sw.keyBuilder.MetricsEpochsTimeline(),
		strconv.FormatInt(last24h, 10),
		strconv.FormatInt(now, 10)).Result()
	if err != nil {
		log.WithError(err).Debug("Failed to count epochs")
		epochsTotal = 0
	}

	// Store participation metrics
	participationMetrics := map[string]interface{}{
		"epochs_participated_24h":  level1Batches,
		"epochs_total_24h":         epochsTotal,
		"participation_rate":       participationRate,
		"level1_batches_24h":       level1Batches,
		"level2_inclusions_24h":    level2Inclusions,
		"inclusion_rate":           inclusionRate,
		"timestamp":                now,
	}

	metricsJSON, _ := json.Marshal(participationMetrics)
	sw.redis.Set(ctx, sw.keyBuilder.MetricsParticipation(), metricsJSON, 60*time.Second)

	log.WithFields(logrus.Fields{
		"participation_rate": fmt.Sprintf("%.1f%%", participationRate),
		"inclusion_rate":     fmt.Sprintf("%.1f%%", inclusionRate),
		"level1_batches":     level1Batches,
		"level2_inclusions":  level2Inclusions,
	}).Debug("Updated participation metrics")
}

// aggregateCurrentEpochStatus detects current epoch and its phase
func (sw *StateWorker) aggregateCurrentEpochStatus(ctx context.Context) {
	// Get latest epoch events from timeline (could be open or close)
	latestEpochs, err := sw.redis.ZRevRangeWithScores(ctx, sw.keyBuilder.MetricsEpochsTimeline(), 0, 4).Result()
	if err != nil || len(latestEpochs) == 0 {
		log.WithError(err).Debug("No epochs found in timeline")
		return
	}

	// Find the most recent open epoch that hasn't been closed yet
	var currentEpochID string
	var currentEpochTimestamp int64
	var epochInfo map[string]string

	now := time.Now().Unix()

	// Check recent timeline entries to find current epoch
	for _, epochEvent := range latestEpochs {
		entry := epochEvent.Member.(string)
		timestamp := int64(epochEvent.Score)
		parts := strings.Split(entry, ":")
		if len(parts) < 2 {
			continue
		}

		eventType := parts[0]
		epochID := parts[1]

		if eventType == "open" {
			// Get epoch info to check if still open
			infoKey := sw.keyBuilder.MetricsEpochInfo(epochID)
			info, _ := sw.redis.HGetAll(ctx, infoKey).Result()

			// Check if this epoch has a corresponding close event
			hasCloseEvent := false
			for _, checkEvent := range latestEpochs {
				checkEntry := checkEvent.Member.(string)
				if strings.HasPrefix(checkEntry, "close:"+epochID) {
					hasCloseEvent = true
					break
				}
			}

			if !hasCloseEvent {
				currentEpochID = epochID
				currentEpochTimestamp = timestamp
				epochInfo = info
				break
			}
		}
	}

	// If no open epoch found, use the most recent epoch
	if currentEpochID == "" {
		// Use the most recent event
		latestEvent := latestEpochs[0]
		entry := latestEvent.Member.(string)
		parts := strings.Split(entry, ":")
		if len(parts) >= 2 {
			currentEpochID = parts[1]
			currentEpochTimestamp = int64(latestEvent.Score)
			infoKey := sw.keyBuilder.MetricsEpochInfo(currentEpochID)
			epochInfo, _ = sw.redis.HGetAll(ctx, infoKey).Result()
		} else {
			return
		}
	}

	// Determine current phase
	phase := "unknown"
	timeRemaining := int64(0)
	submissionsReceived := int64(0)

	// Check if window is still open using epoch info
	if epochInfo != nil {
		if status, ok := epochInfo["status"]; ok && status == "open" {
			phase = "submission"

			// Calculate time remaining
			windowDuration := float64(20) // default 20 seconds
			if durationStr, ok := epochInfo["duration"]; ok {
				if d, err := strconv.ParseFloat(durationStr, 64); err == nil {
					windowDuration = d
				}
			}

			windowEndTime := currentEpochTimestamp + int64(windowDuration)
			timeRemaining = windowEndTime - now
			if timeRemaining < 0 {
				timeRemaining = 0
			}
		} else {
			// Window closed, check for finalization status
			level1Key := sw.keyBuilder.MetricsBatchLocal(currentEpochID)
			level1Exists, _ := sw.redis.Exists(ctx, level1Key).Result()

			if level1Exists > 0 {
				// Check for aggregation
				level2Key := sw.keyBuilder.MetricsBatchAggregated(currentEpochID)
				level2Exists, _ := sw.redis.Exists(ctx, level2Key).Result()

				if level2Exists > 0 {
					phase = "complete"
				} else {
					phase = "aggregation"
				}
			} else {
				phase = "finalization"
			}
		}
	}

	// Count submissions in current epoch (from processed submissions for this epoch)
	epochProcessedKey := sw.keyBuilder.EpochProcessed(currentEpochID)
	if submissionIDs, err := sw.redis.SMembers(ctx, epochProcessedKey).Result(); err == nil {
		submissionsReceived = int64(len(submissionIDs))
	}

	currentEpochStatus := map[string]interface{}{
		"epoch_id":               currentEpochID,
		"phase":                  phase,
		"time_remaining_seconds": timeRemaining,
		"submissions_received":   submissionsReceived,
		"timestamp":              now,
	}

	// Add epoch info if available
	if epochInfo != nil {
		if durationStr, ok := epochInfo["duration"]; ok {
			if d, err := strconv.ParseFloat(durationStr, 64); err == nil {
				currentEpochStatus["window_duration"] = d
			}
		}
		if dataMarket, ok := epochInfo["data_market"]; ok {
			currentEpochStatus["data_market"] = dataMarket
		}
	}

	statusJSON, _ := json.Marshal(currentEpochStatus)
	sw.redis.Set(ctx, sw.keyBuilder.MetricsCurrentEpoch(), statusJSON, 30*time.Second)

	log.WithFields(logrus.Fields{
		"epoch_id":       currentEpochID,
		"phase":          phase,
		"time_remaining": timeRemaining,
		"submissions":    submissionsReceived,
	}).Debug("Updated current epoch status")
}

// Shutdown gracefully stops the worker
func (sw *StateWorker) Shutdown() {
	close(sw.shutdown)
	sw.wg.Wait()

	if sw.pubsub != nil {
		sw.pubsub.Close()
	}

	log.Info("State worker shutdown complete")
}