package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/powerloom/snapshot-sequencer-validator/docs/swagger"
	keys "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
)

var log = logrus.New()

// @title DSV Pipeline Monitor API (Direct Redis)
// @version 3.0
// @description Monitoring API that reads pre-aggregated data from state-tracker worker
// @BasePath /api/v1

type MonitorAPI struct {
	redis      *redis.Client
	ctx        context.Context
	keyBuilder *keys.KeyBuilder
}

// DashboardSummary provides overall system health and metrics
type DashboardSummary struct {
	ValidatorID        string                 `json:"validator_id"`
	Metrics            map[string]interface{} `json:"metrics"`           // From dashboard:summary
	CurrentStats       map[string]interface{} `json:"current_stats"`     // From stats:current
	RecentActivity     map[string]interface{} `json:"recent_activity"`   // 1m and 5m activity
	Timestamp          time.Time              `json:"timestamp"`
}

// HourlyStats provides hourly aggregated statistics
type HourlyStats struct {
	Hours     []map[string]interface{} `json:"hours"`
	Timestamp time.Time                `json:"timestamp"`
}

// DailyStats provides 24-hour aggregated statistics
type DailyStats struct {
	Summary          map[string]interface{} `json:"summary"`
	HourlyBreakdown  []map[string]interface{} `json:"hourly_breakdown"`
	Timestamp        time.Time                `json:"timestamp"`
}

// FinalizedBatch represents a Level 1 or Level 2 batch
type FinalizedBatch struct {
	EpochID        string   `json:"epoch_id"`
	Level          int      `json:"level"` // 1 for local, 2 for aggregated
	ValidatorID    string   `json:"validator_id,omitempty"` // Level 1 only
	ValidatorIDs   []string `json:"validator_ids,omitempty"` // Level 2 only
	ValidatorCount int      `json:"validator_count,omitempty"` // Level 2 only
	ProjectCount   int      `json:"project_count"`
	IPFSCid        string   `json:"ipfs_cid,omitempty"`
	MerkleRoot     string   `json:"merkle_root,omitempty"`
	Timestamp      int64    `json:"timestamp"`
	Type           string   `json:"type"` // "local" or "aggregated"
}

// EpochInfo represents epoch timeline information
type EpochInfo struct {
	EpochID        string `json:"epoch_id"`
	Status         string `json:"status"` // "open", "closed"
	Phase          string `json:"phase"` // "submission", "finalization", "aggregation", "complete"
	StartTime      int64  `json:"start_time"`
	Duration       int    `json:"duration"`
	DataMarket     string `json:"data_market,omitempty"`
	Level1Batch    bool   `json:"level1_batch_exists"`
	Level2Batch    bool   `json:"level2_batch_exists"`
}

// QueueStatus represents queue depth and status
type QueueStatus struct {
	QueueName string `json:"queue_name"`
	Depth     int64  `json:"depth"`
	Status    string `json:"status"` // "empty", "healthy", "moderate", "high", "critical"
}

func NewMonitorAPI(redisClient *redis.Client, protocol, market string) *MonitorAPI {
	return &MonitorAPI{
		redis:      redisClient,
		ctx:        context.Background(),
		keyBuilder: keys.NewKeyBuilder(protocol, market),
	}
}

// @Summary Dashboard summary
// @Description Get pre-aggregated dashboard metrics from state-tracker
// @Tags dashboard
// @Produce json
// @Success 200 {object} DashboardSummary
// @Router /dashboard/summary [get]
func (m *MonitorAPI) DashboardSummary(c *gin.Context) {
	// Read pre-aggregated dashboard summary from state-tracker
	summaryJSON, err := m.redis.Get(m.ctx, "dashboard:summary").Result()
	if err != nil && err != redis.Nil {
		log.WithError(err).Error("Failed to fetch dashboard summary")
	}

	var summary map[string]interface{}
	if summaryJSON != "" {
		json.Unmarshal([]byte(summaryJSON), &summary)
	} else {
		summary = make(map[string]interface{})
	}

	// Read current stats hash from state-tracker
	currentStats, err := m.redis.HGetAll(m.ctx, "stats:current").Result()
	if err != nil && err != redis.Nil {
		log.WithError(err).Error("Failed to fetch current stats")
	}

	// Convert string map to interface map
	statsMap := make(map[string]interface{})
	for k, v := range currentStats {
		// Try to parse as number
		if num, err := strconv.ParseFloat(v, 64); err == nil {
			statsMap[k] = num
		} else {
			statsMap[k] = v
		}
	}

	// Get queue depths for real-time status
	submissionQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.SubmissionQueue()).Result()
	finalizationQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.FinalizationQueue()).Result()
	aggregationQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.AggregationQueue()).Result()

	// Add queue depths to current stats
	statsMap["submission_queue_depth"] = submissionQueueDepth
	statsMap["finalization_queue_depth"] = finalizationQueueDepth
	statsMap["aggregation_queue_depth"] = aggregationQueueDepth

	// Get participation metrics from state-tracker
	participationJSON, _ := m.redis.Get(m.ctx, "metrics:participation").Result()
	var participation map[string]interface{}
	if participationJSON != "" {
		json.Unmarshal([]byte(participationJSON), &participation)
	}

	// Get current epoch status from state-tracker
	currentEpochJSON, _ := m.redis.Get(m.ctx, "metrics:current_epoch").Result()
	var currentEpoch map[string]interface{}
	if currentEpochJSON != "" {
		json.Unmarshal([]byte(currentEpochJSON), &currentEpoch)
	}

	response := DashboardSummary{
		ValidatorID:    getEnv("VALIDATOR_ID", "validator-001"),
		Metrics:        summary,
		CurrentStats:   statsMap,
		RecentActivity: make(map[string]interface{}),
		Timestamp:      time.Now(),
	}

	// Add recent activity from pre-aggregated summary
	if summary != nil {
		response.RecentActivity["submissions_1m"] = summary["submissions_1m"]
		response.RecentActivity["submissions_5m"] = summary["submissions_5m"]
		response.RecentActivity["epochs_1m"] = summary["epochs_1m"]
		response.RecentActivity["epochs_5m"] = summary["epochs_5m"]
		response.RecentActivity["batches_1m"] = summary["batches_1m"]
		response.RecentActivity["batches_5m"] = summary["batches_5m"]
	}

	// Add participation metrics (state-tracker should provide these)
	if participation != nil {
		response.Metrics["participation_rate"] = participation["participation_rate"]
		response.Metrics["inclusion_rate"] = participation["inclusion_rate"]
		response.Metrics["level1_batches_24h"] = participation["level1_batches_24h"]
		response.Metrics["level2_inclusions_24h"] = participation["level2_inclusions_24h"]
		response.Metrics["epochs_participated_24h"] = participation["epochs_participated_24h"]
		response.Metrics["epochs_total_24h"] = participation["epochs_total_24h"]
	} else {
		// Fallback: set to 0 if state-tracker hasn't calculated yet
		response.Metrics["level1_batches_24h"] = 0
		response.Metrics["level2_inclusions_24h"] = 0
		response.Metrics["participation_rate"] = 0
		response.Metrics["inclusion_rate"] = 0
	}

	// Remove validation_rate - it's not a real metric
	delete(response.Metrics, "validation_rate")
	delete(response.Metrics, "validations_total")

	// Add current epoch status
	if currentEpoch != nil {
		response.CurrentStats["current_epoch_id"] = currentEpoch["epoch_id"]
		response.CurrentStats["current_epoch_phase"] = currentEpoch["phase"]
		response.CurrentStats["epoch_time_remaining"] = currentEpoch["time_remaining_seconds"]
		response.CurrentStats["epoch_window_duration"] = currentEpoch["window_duration"]
	}

	c.JSON(http.StatusOK, response)
}

// @Summary Hourly statistics
// @Description Get pre-aggregated hourly statistics
// @Tags stats
// @Produce json
// @Param hours query int false "Number of hours to retrieve (default 24)"
// @Success 200 {object} HourlyStats
// @Router /stats/hourly [get]
func (m *MonitorAPI) HourlyStats(c *gin.Context) {
	hoursParam := c.DefaultQuery("hours", "24")
	hours, _ := strconv.Atoi(hoursParam)
	if hours <= 0 || hours > 48 {
		hours = 24
	}

	hourlyData := make([]map[string]interface{}, 0, hours)
	now := time.Now()

	// Fetch hourly stats for requested hours
	for i := 0; i < hours; i++ {
		hourTime := now.Add(-time.Duration(i) * time.Hour).Truncate(time.Hour)
		hourKey := fmt.Sprintf("stats:hourly:%d", hourTime.Unix())

		statsJSON, err := m.redis.Get(m.ctx, hourKey).Result()
		if err == redis.Nil {
			continue // No data for this hour
		} else if err != nil {
			log.WithError(err).WithField("key", hourKey).Debug("Failed to fetch hourly stats")
			continue
		}

		var hourStats map[string]interface{}
		if err := json.Unmarshal([]byte(statsJSON), &hourStats); err == nil {
			hourlyData = append(hourlyData, hourStats)
		}
	}

	response := HourlyStats{
		Hours:     hourlyData,
		Timestamp: time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

// @Summary Daily statistics
// @Description Get pre-aggregated 24-hour statistics
// @Tags stats
// @Produce json
// @Success 200 {object} DailyStats
// @Router /stats/daily [get]
func (m *MonitorAPI) DailyStats(c *gin.Context) {
	// Read pre-aggregated daily stats
	statsJSON, err := m.redis.Get(m.ctx, "stats:daily").Result()
	if err == redis.Nil {
		c.JSON(http.StatusOK, DailyStats{
			Summary:   map[string]interface{}{"message": "No daily stats available yet"},
			Timestamp: time.Now(),
		})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch daily stats"})
		return
	}

	var dailyStats map[string]interface{}
	if err := json.Unmarshal([]byte(statsJSON), &dailyStats); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse daily stats"})
		return
	}

	// Extract hourly breakdown if present
	var hourlyBreakdown []map[string]interface{}
	if breakdown, ok := dailyStats["hourly_breakdown"].([]interface{}); ok {
		for _, hour := range breakdown {
			if hourMap, ok := hour.(map[string]interface{}); ok {
				hourlyBreakdown = append(hourlyBreakdown, hourMap)
			}
		}
		delete(dailyStats, "hourly_breakdown") // Remove from summary to avoid duplication
	}

	response := DailyStats{
		Summary:         dailyStats,
		HourlyBreakdown: hourlyBreakdown,
		Timestamp:       time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

// @Summary Pipeline overview (legacy support)
// @Description Get pipeline metrics from pre-aggregated data
// @Tags pipeline
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /pipeline/overview [get]
func (m *MonitorAPI) PipelineOverview(c *gin.Context) {
	// Read from dashboard:summary for compatibility
	summaryJSON, _ := m.redis.Get(m.ctx, "dashboard:summary").Result()

	var summary map[string]interface{}
	if summaryJSON != "" {
		json.Unmarshal([]byte(summaryJSON), &summary)
	} else {
		summary = make(map[string]interface{})
	}

	// Add queue depths if needed (real-time check)
	submissionQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.SubmissionQueue()).Result()
	finalizationQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.FinalizationQueue()).Result()
	aggregationQueueDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.AggregationQueue()).Result()

	overview := map[string]interface{}{
		"submission_queue": map[string]interface{}{
			"depth":  submissionQueueDepth,
			"status": getQueueStatus(int(submissionQueueDepth)),
		},
		"finalization_queue": map[string]interface{}{
			"depth":  finalizationQueueDepth,
			"status": getQueueStatus(int(finalizationQueueDepth)),
		},
		"aggregation_queue": map[string]interface{}{
			"depth":  aggregationQueueDepth,
			"status": getQueueStatus(int(aggregationQueueDepth)),
		},
		"metrics":   summary,
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, overview)
}

// @Summary Recent timeline
// @Description Get recent events from timeline sorted sets
// @Tags timeline
// @Produce json
// @Param type query string false "Event type (submission, validation, epoch, batch)"
// @Param minutes query int false "Minutes to look back (default 5)"
// @Success 200 {object} map[string]interface{}
// @Router /timeline/recent [get]
func (m *MonitorAPI) RecentTimeline(c *gin.Context) {
	eventType := c.DefaultQuery("type", "submission")
	minutesParam := c.DefaultQuery("minutes", "5")
	minutes, _ := strconv.Atoi(minutesParam)
	if minutes <= 0 || minutes > 60 {
		minutes = 5
	}

	// Validate event type
	validTypes := map[string]bool{
		"submission": true,
		"validation": true,
		"epoch":      true,
		"batch":      true,
	}
	if !validTypes[eventType] {
		eventType = "submission"
	}

	timelineKey := fmt.Sprintf("timeline:%s", eventType)
	now := time.Now().Unix()
	start := now - int64(minutes*60)

	// Get recent events from sorted set
	events, err := m.redis.ZRangeByScoreWithScores(m.ctx, timelineKey, &redis.ZRangeBy{
		Min: strconv.FormatInt(start, 10),
		Max: strconv.FormatInt(now, 10),
	}).Result()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Format events
	formattedEvents := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		formattedEvents = append(formattedEvents, map[string]interface{}{
			"entity_id": event.Member,
			"timestamp": int64(event.Score),
			"time":      time.Unix(int64(event.Score), 0).Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"type":      eventType,
		"minutes":   minutes,
		"count":     len(formattedEvents),
		"events":    formattedEvents,
		"timestamp": time.Now(),
	})
}

// @Summary Health check
// @Description Check if the API and Redis connection are healthy
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (m *MonitorAPI) Health(c *gin.Context) {
	if err := m.redis.Ping(m.ctx).Err(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	// Check if state-tracker data is fresh
	summaryJSON, _ := m.redis.Get(m.ctx, "dashboard:summary").Result()
	dataFresh := summaryJSON != ""

	c.JSON(http.StatusOK, gin.H{
		"status":      "healthy",
		"redis":       "connected",
		"data_fresh":  dataFresh,
		"timestamp":   time.Now(),
	})
}

// @Summary Finalized batches
// @Description Get Level 1 (local) or Level 2 (aggregated) finalized batches
// @Tags batches
// @Produce json
// @Param level query int false "Batch level (1 or 2, default both)"
// @Param epoch_id query string false "Specific epoch ID"
// @Param limit query int false "Number of batches to retrieve (default 50)"
// @Success 200 {array} FinalizedBatch
// @Router /batches/finalized [get]
func (m *MonitorAPI) FinalizedBatches(c *gin.Context) {
	levelParam := c.DefaultQuery("level", "0")
	level, _ := strconv.Atoi(levelParam)
	epochID := c.Query("epoch_id")
	limitParam := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitParam)
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var batches []FinalizedBatch

	// If specific epoch requested
	if epochID != "" {
		if level == 1 || level == 0 {
			// Get Level 1 batch
			level1Key := fmt.Sprintf("metrics:batch:local:%s", epochID)
			level1Data, err := m.redis.Get(m.ctx, level1Key).Result()
			if err == nil {
				var batchData map[string]interface{}
				if json.Unmarshal([]byte(level1Data), &batchData) == nil {
					batch := FinalizedBatch{
						EpochID:      epochID,
						Level:        1,
						Type:         "local",
						ProjectCount: int(batchData["project_count"].(float64)),
						Timestamp:    int64(batchData["timestamp"].(float64)),
					}
					if vid, ok := batchData["validator_id"].(string); ok {
						batch.ValidatorID = vid
					}
					if cid, ok := batchData["ipfs_cid"].(string); ok {
						batch.IPFSCid = cid
					}
					batches = append(batches, batch)
				}
			}
		}

		if level == 2 || level == 0 {
			// Get Level 2 batch
			level2Key := fmt.Sprintf("metrics:batch:aggregated:%s", epochID)
			level2Data, err := m.redis.Get(m.ctx, level2Key).Result()
			if err == nil {
				var batchData map[string]interface{}
				if json.Unmarshal([]byte(level2Data), &batchData) == nil {
					// Get validator list
					validatorsKey := fmt.Sprintf("metrics:batch:%s:validators", epochID)
					validatorsJSON, _ := m.redis.Get(m.ctx, validatorsKey).Result()
					var validators []string
					if validatorsJSON != "" {
						json.Unmarshal([]byte(validatorsJSON), &validators)
					}

					batch := FinalizedBatch{
						EpochID:        epochID,
						Level:          2,
						Type:           "aggregated",
						ValidatorIDs:   validators,
						ValidatorCount: len(validators),
						ProjectCount:   int(batchData["project_count"].(float64)),
						Timestamp:      int64(batchData["timestamp"].(float64)),
					}
					batches = append(batches, batch)
				}
			}
		}
	} else {
		// Get recent batches from timeline
		now := time.Now().Unix()
		start := now - 3600 // Last hour by default

		entries, _ := m.redis.ZRevRangeByScore(m.ctx, "metrics:batches:timeline", &redis.ZRangeBy{
			Min:   strconv.FormatInt(start, 10),
			Max:   "+inf",
			Count: int64(limit),
		}).Result()

		for _, entry := range entries {
			parts := strings.Split(entry, ":")
			if len(parts) < 2 {
				continue
			}
			batchType := parts[0]
			batchEpoch := parts[1]

			if batchType == "local" && (level == 1 || level == 0) {
				level1Key := fmt.Sprintf("metrics:batch:local:%s", batchEpoch)
				level1Data, err := m.redis.Get(m.ctx, level1Key).Result()
				if err == nil {
					var batchData map[string]interface{}
					if json.Unmarshal([]byte(level1Data), &batchData) == nil {
						batch := FinalizedBatch{
							EpochID:      batchEpoch,
							Level:        1,
							Type:         "local",
							ProjectCount: int(batchData["project_count"].(float64)),
							Timestamp:    int64(batchData["timestamp"].(float64)),
						}
						if vid, ok := batchData["validator_id"].(string); ok {
							batch.ValidatorID = vid
						}
						if cid, ok := batchData["ipfs_cid"].(string); ok {
							batch.IPFSCid = cid
						}
						batches = append(batches, batch)
					}
				}
			} else if batchType == "aggregated" && (level == 2 || level == 0) {
				level2Key := fmt.Sprintf("metrics:batch:aggregated:%s", batchEpoch)
				level2Data, err := m.redis.Get(m.ctx, level2Key).Result()
				if err == nil {
					var batchData map[string]interface{}
					if json.Unmarshal([]byte(level2Data), &batchData) == nil {
						validatorsKey := fmt.Sprintf("metrics:batch:%s:validators", batchEpoch)
						validatorsJSON, _ := m.redis.Get(m.ctx, validatorsKey).Result()
						var validators []string
						if validatorsJSON != "" {
							json.Unmarshal([]byte(validatorsJSON), &validators)
						}

						batch := FinalizedBatch{
							EpochID:        batchEpoch,
							Level:          2,
							Type:           "aggregated",
							ValidatorIDs:   validators,
							ValidatorCount: len(validators),
							ProjectCount:   int(batchData["project_count"].(float64)),
							Timestamp:      int64(batchData["timestamp"].(float64)),
						}
						batches = append(batches, batch)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, batches)
}

// @Summary Aggregation results
// @Description Get network-wide aggregated batches with validator contributions
// @Tags aggregation
// @Produce json
// @Param limit query int false "Number of results (default 20)"
// @Success 200 {array} FinalizedBatch
// @Router /aggregation/results [get]
func (m *MonitorAPI) AggregationResults(c *gin.Context) {
	limitParam := c.DefaultQuery("limit", "20")
	limit, _ := strconv.Atoi(limitParam)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Get recent Level 2 aggregated batches from timeline
	now := time.Now().Unix()
	start := now - 86400 // Last 24 hours

	entries, _ := m.redis.ZRevRangeByScore(m.ctx, "metrics:batches:timeline", &redis.ZRangeBy{
		Min:   strconv.FormatInt(start, 10),
		Max:   "+inf",
		Count: int64(limit * 2), // Get more to filter only aggregated
	}).Result()

	var batches []FinalizedBatch
	for _, entry := range entries {
		if !strings.HasPrefix(entry, "aggregated:") {
			continue
		}

		epochID := strings.TrimPrefix(entry, "aggregated:")

		level2Key := fmt.Sprintf("metrics:batch:aggregated:%s", epochID)
		level2Data, err := m.redis.Get(m.ctx, level2Key).Result()
		if err != nil {
			continue
		}

		var batchData map[string]interface{}
		if json.Unmarshal([]byte(level2Data), &batchData) != nil {
			continue
		}

		// Get validator list
		validatorsKey := fmt.Sprintf("metrics:batch:%s:validators", epochID)
		validatorsJSON, _ := m.redis.Get(m.ctx, validatorsKey).Result()
		var validators []string
		if validatorsJSON != "" {
			json.Unmarshal([]byte(validatorsJSON), &validators)
		}

		batch := FinalizedBatch{
			EpochID:        epochID,
			Level:          2,
			Type:           "aggregated",
			ValidatorIDs:   validators,
			ValidatorCount: len(validators),
			ProjectCount:   int(batchData["project_count"].(float64)),
			Timestamp:      int64(batchData["timestamp"].(float64)),
		}

		batches = append(batches, batch)

		if len(batches) >= limit {
			break
		}
	}

	c.JSON(http.StatusOK, batches)
}

// @Summary Epochs timeline
// @Description Get epoch progression with phases and batch status
// @Tags epochs
// @Produce json
// @Param limit query int false "Number of epochs (default 50)"
// @Success 200 {array} EpochInfo
// @Router /epochs/timeline [get]
func (m *MonitorAPI) EpochsTimeline(c *gin.Context) {
	limitParam := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitParam)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Get recent epochs from timeline (entries are "open:{id}" or "close:{id}")
	entries, _ := m.redis.ZRevRange(m.ctx, "metrics:epochs:timeline", 0, int64(limit*2)).Result()

	epochMap := make(map[string]*EpochInfo)
	var epochOrder []string

	for _, entry := range entries {
		parts := strings.Split(entry, ":")
		if len(parts) < 2 {
			continue
		}
		action := parts[0]
		epochID := parts[1]

		if _, exists := epochMap[epochID]; !exists {
			epochMap[epochID] = &EpochInfo{
				EpochID: epochID,
			}
			epochOrder = append(epochOrder, epochID)
		}

		if action == "open" {
			epochMap[epochID].Status = "open"
		} else if action == "close" {
			epochMap[epochID].Status = "closed"
		}
	}

	// Get detailed info for each epoch
	var epochs []EpochInfo
	for _, epochID := range epochOrder {
		if len(epochs) >= limit {
			break
		}

		epochInfo := epochMap[epochID]

		// Get epoch info hash
		infoKey := fmt.Sprintf("metrics:epoch:%s:info", epochID)
		infoData, err := m.redis.HGetAll(m.ctx, infoKey).Result()
		if err == nil {
			if startStr, ok := infoData["start"]; ok {
				if startInt, err := strconv.ParseInt(startStr, 10, 64); err == nil {
					epochInfo.StartTime = startInt
				}
			}
			if durStr, ok := infoData["duration"]; ok {
				if durInt, err := strconv.Atoi(durStr); err == nil {
					epochInfo.Duration = durInt
				}
			}
			if market, ok := infoData["data_market"]; ok {
				epochInfo.DataMarket = market
			}
		}

		// Check for Level 1 batch
		level1Key := fmt.Sprintf("metrics:batch:local:%s", epochID)
		level1Exists, _ := m.redis.Exists(m.ctx, level1Key).Result()
		epochInfo.Level1Batch = level1Exists > 0

		// Check for Level 2 batch
		level2Key := fmt.Sprintf("metrics:batch:aggregated:%s", epochID)
		level2Exists, _ := m.redis.Exists(m.ctx, level2Key).Result()
		epochInfo.Level2Batch = level2Exists > 0

		// Determine phase
		if epochInfo.Status == "open" {
			epochInfo.Phase = "submission"
		} else if epochInfo.Level2Batch {
			epochInfo.Phase = "complete"
		} else if epochInfo.Level1Batch {
			epochInfo.Phase = "aggregation"
		} else {
			epochInfo.Phase = "finalization"
		}

		epochs = append(epochs, *epochInfo)
	}

	c.JSON(http.StatusOK, epochs)
}

// @Summary Queue status
// @Description Get real-time queue depths and processing rates
// @Tags queues
// @Produce json
// @Success 200 {array} QueueStatus
// @Router /queues/status [get]
func (m *MonitorAPI) QueuesStatus(c *gin.Context) {
	queues := []QueueStatus{
		{
			QueueName: "submission_queue",
			Depth:     0,
			Status:    "empty",
		},
		{
			QueueName: "finalization_queue",
			Depth:     0,
			Status:    "empty",
		},
		{
			QueueName: "aggregation_queue",
			Depth:     0,
			Status:    "empty",
		},
	}

	// Get actual queue depths
	submissionDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.SubmissionQueue()).Result()
	queues[0].Depth = submissionDepth
	queues[0].Status = getQueueStatus(int(submissionDepth))

	finalizationDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.FinalizationQueue()).Result()
	queues[1].Depth = finalizationDepth
	queues[1].Status = getQueueStatus(int(finalizationDepth))

	aggregationDepth, _ := m.redis.LLen(m.ctx, m.keyBuilder.AggregationQueue()).Result()
	queues[2].Depth = aggregationDepth
	queues[2].Status = getQueueStatus(int(aggregationDepth))

	c.JSON(http.StatusOK, queues)
}



// Helper function to determine queue status
func getQueueStatus(depth int) string {
	switch {
	case depth == 0:
		return "empty"
	case depth < 100:
		return "healthy"
	case depth < 500:
		return "moderate"
	case depth < 1000:
		return "high"
	default:
		return "critical"
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	// Configure logger
	log.SetFormatter(&logrus.JSONFormatter{})
	if os.Getenv("LOG_LEVEL") == "debug" {
		log.SetLevel(logrus.DebugLevel)
	} else {
		log.SetLevel(logrus.InfoLevel)
	}

	// Get configuration
	redisHost := getEnv("REDIS_HOST", "localhost")
	redisPort := getEnv("REDIS_PORT", "6379")
	protocol := getEnv("PROTOCOL", "uniswapv2")

	// Parse DATA_MARKET_ADDRESSES (could be comma-separated or JSON array)
	marketsEnv := getEnv("MARKET", "")
	if marketsEnv == "" {
		marketsEnv = getEnv("DATA_MARKET_ADDRESSES", "0x21cb57C1f2352ad215a463DD867b838749CD3b8f")
	}

	// Extract first market address (handle comma-separated or JSON array)
	market := marketsEnv
	if strings.Contains(marketsEnv, ",") {
		market = strings.Split(marketsEnv, ",")[0]
	} else if strings.HasPrefix(marketsEnv, "[") {
		// JSON array - extract first address
		marketsEnv = strings.Trim(marketsEnv, "[]")
		marketsEnv = strings.ReplaceAll(marketsEnv, "\"", "")
		if strings.Contains(marketsEnv, ",") {
			market = strings.Split(marketsEnv, ",")[0]
		}
	}
	market = strings.TrimSpace(market)

	port := getEnv("MONITOR_API_PORT", "8080")

	// Connect to Redis
	ctx := context.Background()
	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", redisHost, redisPort),
	})

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.WithFields(logrus.Fields{
		"redis":    fmt.Sprintf("%s:%s", redisHost, redisPort),
		"protocol": protocol,
		"market":   market,
	}).Info("Monitor API connected to Redis")

	// Create API instance
	api := NewMonitorAPI(redisClient, protocol, market)

	// Setup routes
	router := gin.Default()

	v1 := router.Group("/api/v1")
	{
		// Health check
		v1.GET("/health", api.Health)

		// Dashboard and stats endpoints
		v1.GET("/dashboard/summary", api.DashboardSummary)
		v1.GET("/stats/hourly", api.HourlyStats)
		v1.GET("/stats/daily", api.DailyStats)

		// P0/P1 Endpoints - Core visibility
		v1.GET("/batches/finalized", api.FinalizedBatches)
		v1.GET("/aggregation/results", api.AggregationResults)
		v1.GET("/epochs/timeline", api.EpochsTimeline)
		v1.GET("/queues/status", api.QueuesStatus)

		// Timeline events
		v1.GET("/timeline/recent", api.RecentTimeline)

		// Legacy compatibility endpoint
		v1.GET("/pipeline/overview", api.PipelineOverview)
	}

	// Swagger documentation
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	log.WithFields(logrus.Fields{
		"port":    port,
		"swagger": fmt.Sprintf("http://localhost:%s/swagger/index.html", port),
	}).Info("🚀 Monitor API (Direct Redis) starting")

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}