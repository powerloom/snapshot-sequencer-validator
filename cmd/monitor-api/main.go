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
	// Read pre-aggregated dashboard summary
	summaryJSON, err := m.redis.Get(m.ctx, "dashboard:summary").Result()
	if err != nil && err != redis.Nil {
		log.WithError(err).Error("Failed to fetch dashboard summary")
	}

	var summary map[string]interface{}
	if summaryJSON != "" {
		json.Unmarshal([]byte(summaryJSON), &summary)
	}

	// Read current stats hash
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

	response := DashboardSummary{
		ValidatorID:    getEnv("VALIDATOR_ID", "validator-001"),
		Metrics:        summary,
		CurrentStats:   statsMap,
		RecentActivity: make(map[string]interface{}),
		Timestamp:      time.Now(),
	}

	// Add recent activity from summary
	if summary != nil {
		response.RecentActivity["submissions_1m"] = summary["submissions_1m"]
		response.RecentActivity["submissions_5m"] = summary["submissions_5m"]
		response.RecentActivity["epochs_1m"] = summary["epochs_1m"]
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
		// Primary endpoints (read from state-tracker prepared data)
		v1.GET("/health", api.Health)
		v1.GET("/dashboard/summary", api.DashboardSummary)
		v1.GET("/stats/hourly", api.HourlyStats)
		v1.GET("/stats/daily", api.DailyStats)
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