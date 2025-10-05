package main

import (
	"context"
	"encoding/json"
	"fmt"
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
func NewStateWorker(redisClient *redis.Client) *StateWorker {
	protocol := viper.GetString("protocol")
	market := viper.GetString("market")
	keyBuilder := redislib.NewKeyBuilder(protocol, market)

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

	// Calculate rates
	duration := time.Since(sw.lastReset).Seconds()
	submissionRate := float64(sw.submissions) / duration
	validationRate := float64(sw.validations) / duration
	epochRate := float64(sw.epochs) / duration
	batchRate := float64(sw.batches) / duration

	sw.mu.RUnlock()

	// Count active validators using two-level reference (no SCAN)
	// 1. Get recent batches from timeline
	// 2. Read validator lists from each batch
	activeValidators := make(map[string]bool)
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute).Unix()

	// Get recent batch epochs from timeline
	recentBatches, _ := sw.redis.ZRangeByScore(ctx, "metrics:batches:timeline", &redis.ZRangeBy{
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

		validatorsKey := fmt.Sprintf("metrics:batch:%s:validators", epochID)
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

	// Prepare summary data
	summary := map[string]interface{}{
		"submissions_total":     sw.submissions,
		"validations_total":     sw.validations,
		"epochs_total":          sw.epochs,
		"batches_total":         sw.batches,
		"submission_rate":       submissionRate,
		"validation_rate":       validationRate,
		"epoch_rate":            epochRate,
		"batch_rate":            batchRate,
		"active_validators":     activeCount,
		"updated_at":           time.Now().Unix(),
		"measurement_duration": duration,
	}

	// Get recent activity counts from timelines
	nowTS := time.Now().Unix()
	oneMinuteAgo := nowTS - 60
	fiveMinutesAgoTS := nowTS - 300

	// Count recent submissions
	recentSubmissions, _ := sw.redis.ZCount(ctx, "metrics:submissions:timeline",
		strconv.FormatInt(oneMinuteAgo, 10),
		strconv.FormatInt(nowTS, 10)).Result()
	summary["submissions_1m"] = recentSubmissions

	recentSubmissions5m, _ := sw.redis.ZCount(ctx, "metrics:submissions:timeline",
		strconv.FormatInt(fiveMinutesAgoTS, 10),
		strconv.FormatInt(nowTS, 10)).Result()
	summary["submissions_5m"] = recentSubmissions5m

	// Count recent epochs
	recentEpochs, _ := sw.redis.ZCount(ctx, "metrics:epochs:timeline",
		strconv.FormatInt(oneMinuteAgo, 10),
		strconv.FormatInt(nowTS, 10)).Result()
	summary["epochs_1m"] = recentEpochs

	// Store summary with TTL
	summaryJSON, _ := json.Marshal(summary)
	sw.redis.Set(ctx, "dashboard:summary", summaryJSON, 60*time.Second)

	// Also store as hash for easier access
	sw.redis.HMSet(ctx, "stats:current", summary)
	sw.redis.Expire(ctx, "stats:current", 60*time.Second)

	datasetsGenerated.WithLabelValues("dashboard_summary").Inc()

	log.WithFields(logrus.Fields{
		"submissions": sw.submissions,
		"epochs":      sw.epochs,
		"validators":  activeCount,
	}).Debug("Updated dashboard summary")
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

	// Count events in this hour
	submissions, _ := sw.redis.ZCount(ctx, "metrics:submissions:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	validations, _ := sw.redis.ZCount(ctx, "metrics:validations:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	epochs, _ := sw.redis.ZCount(ctx, "metrics:epochs:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	batches, _ := sw.redis.ZCount(ctx, "metrics:batches:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	// Count unique validators using two-level reference (no SCAN)
	uniqueValidatorsMap := make(map[string]bool)
	hourBatches, _ := sw.redis.ZRangeByScore(ctx, "metrics:batches:timeline", &redis.ZRangeBy{
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

		validatorsKey := fmt.Sprintf("metrics:batch:%s:validators", epochID)
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
		"submissions":       submissions,
		"validations":       validations,
		"epochs":            epochs,
		"batches":           batches,
		"unique_validators": uniqueValidators,
		"updated_at":        time.Now().Unix(),
	}

	// Store with hour as key
	hourKey := fmt.Sprintf("stats:hourly:%d", hourStart.Unix())
	statsJSON, _ := json.Marshal(hourStats)
	sw.redis.Set(ctx, hourKey, statsJSON, 2*time.Hour)

	log.WithFields(logrus.Fields{
		"hour":        hourStart.Format("15:04"),
		"submissions": submissions,
		"epochs":      epochs,
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
	submissions, _ := sw.redis.ZCount(ctx, "metrics:submissions:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	validations, _ := sw.redis.ZCount(ctx, "metrics:validations:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	epochs, _ := sw.redis.ZCount(ctx, "metrics:epochs:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	batches, _ := sw.redis.ZCount(ctx, "metrics:batches:timeline",
		strconv.FormatInt(startTS, 10),
		strconv.FormatInt(endTS, 10)).Result()

	// Get hourly breakdown
	hourlyBreakdown := make([]map[string]interface{}, 0, 24)
	for i := 0; i < 24; i++ {
		hourStart := dayAgo.Add(time.Duration(i) * time.Hour)
		hourEnd := hourStart.Add(time.Hour)

		hourSubmissions, _ := sw.redis.ZCount(ctx, "metrics:submissions:timeline",
			strconv.FormatInt(hourStart.Unix(), 10),
			strconv.FormatInt(hourEnd.Unix(), 10)).Result()

		hourEpochs, _ := sw.redis.ZCount(ctx, "metrics:epochs:timeline",
			strconv.FormatInt(hourStart.Unix(), 10),
			strconv.FormatInt(hourEnd.Unix(), 10)).Result()

		hourlyBreakdown = append(hourlyBreakdown, map[string]interface{}{
			"hour":        hourStart.Format("15:04"),
			"submissions": hourSubmissions,
			"epochs":      hourEpochs,
		})
	}

	dailyStats := map[string]interface{}{
		"period_start":      dayAgo.Format(time.RFC3339),
		"period_end":        now.Format(time.RFC3339),
		"submissions_total": submissions,
		"validations_total": validations,
		"epochs_total":      epochs,
		"batches_total":     batches,
		"hourly_breakdown":  hourlyBreakdown,
		"updated_at":        time.Now().Unix(),
	}

	// Store daily stats
	statsJSON, _ := json.Marshal(dailyStats)
	sw.redis.Set(ctx, "stats:daily", statsJSON, 24*time.Hour)

	datasetsGenerated.WithLabelValues("daily_stats").Inc()

	log.WithFields(logrus.Fields{
		"submissions": submissions,
		"epochs":      epochs,
		"batches":     batches,
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
		"metrics:submissions:timeline",
		"metrics:validations:timeline",
		"metrics:epochs:timeline",
		"metrics:batches:timeline",
		"metrics:parts:timeline",
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

// Shutdown gracefully stops the worker
func (sw *StateWorker) Shutdown() {
	close(sw.shutdown)
	sw.wg.Wait()

	if sw.pubsub != nil {
		sw.pubsub.Close()
	}

	log.Info("State worker shutdown complete")
}