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

// @title DSV Pipeline Monitor API
// @version 1.0
// @description Monitoring API for Decentralized Sequencer Validator pipeline
// @BasePath /api/v1

type MonitorAPI struct {
	redis      *redis.Client
	ctx        context.Context
	keyBuilder *keys.KeyBuilder
}

type PipelineOverview struct {
	SubmissionQueue    QueueStatus       `json:"submission_queue"`
	ActiveWindows      int               `json:"active_windows"`
	ReadyBatches       int               `json:"ready_batches"`
	FinalizationQueue  QueueStatus       `json:"finalization_queue"`
	ActiveWorkers      int               `json:"active_workers"`
	CompletedParts     int               `json:"completed_parts"`
	FinalizedBatches   int               `json:"finalized_batches"`
	AggregationQueue   QueueStatus       `json:"aggregation_queue"`
	AggregatedBatches  int               `json:"aggregated_batches"`
	Timestamp          time.Time         `json:"timestamp"`
}

type QueueStatus struct {
	Depth  int    `json:"depth"`
	Status string `json:"status"`
}

type SubmissionWindow struct {
	EpochID     string    `json:"epoch_id"`
	Market      string    `json:"market"`
	Status      string    `json:"status"`
	TTL         int64     `json:"ttl_seconds"`
	ClosedAt    time.Time `json:"closed_at,omitempty"`
}

type ReadyBatch struct {
	EpochID      string `json:"epoch_id"`
	ProjectCount int    `json:"project_count"`
	HasVotes     bool   `json:"has_votes"`
}

type WorkerStatus struct {
	WorkerID     string    `json:"worker_id"`
	Status       string    `json:"status"`
	EpochID      string    `json:"epoch_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Healthy      bool      `json:"healthy"`
}

type BatchPart struct {
	EpochID      string `json:"epoch_id"`
	PartID       int    `json:"part_id"`
	ProjectCount int    `json:"project_count"`
}

type FinalizedBatch struct {
	EpochID      string    `json:"epoch_id"`
	ProjectCount int       `json:"project_count"`
	VoteCount    int       `json:"vote_count"`
	SequencerID  string    `json:"sequencer_id"`
	IPFSCid      string    `json:"ipfs_cid,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

type AggregatedBatch struct {
	EpochID       string    `json:"epoch_id"`
	ProjectCount  int       `json:"project_count"`
	ValidatorCount int      `json:"validator_count"`
	Timestamp     time.Time `json:"timestamp"`
}

func main() {
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	log.SetLevel(logrus.InfoLevel)

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

	ctx := context.Background()

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", redisHost, redisPort),
	})

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Infof("Connected to Redis at %s:%s", redisHost, redisPort)

	keyBuilder := keys.NewKeyBuilder(protocol, market)

	api := &MonitorAPI{
		redis:      redisClient,
		ctx:        ctx,
		keyBuilder: keyBuilder,
	}

	router := gin.Default()

	v1 := router.Group("/api/v1")
	{
		v1.GET("/health", api.Health)
		v1.GET("/pipeline/overview", api.PipelineOverview)
		v1.GET("/submissions/windows", api.SubmissionWindows)
		v1.GET("/submissions/queue", api.SubmissionQueue)
		v1.GET("/batches/ready", api.ReadyBatches)
		v1.GET("/batches/finalization-queue", api.FinalizationQueue)
		v1.GET("/workers/status", api.WorkerStatus)
		v1.GET("/batches/parts", api.BatchParts)
		v1.GET("/batches/finalized", api.FinalizedBatches)
		v1.GET("/aggregation/queue", api.AggregationQueue)
		v1.GET("/aggregation/results", api.AggregatedBatches)
	}

	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	log.Infof("🚀 Monitor API starting on port %s", port)
	log.Infof("📊 Swagger UI available at http://localhost:%s/swagger/index.html", port)

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// @Summary Health check
// @Description Check if the API is running and can connect to Redis
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

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"timestamp": time.Now(),
	})
}

// @Summary Pipeline overview
// @Description Get overall pipeline status summary
// @Tags pipeline
// @Produce json
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Success 200 {object} PipelineOverview
// @Router /pipeline/overview [get]
func (m *MonitorAPI) PipelineOverview(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	overview := PipelineOverview{
		Timestamp: time.Now(),
	}

	// Submission queue depth
	queueKey := kb.SubmissionQueue()
	if depth, err := m.redis.LLen(m.ctx, queueKey).Result(); err == nil {
		overview.SubmissionQueue = QueueStatus{
			Depth:  int(depth),
			Status: getQueueStatus(int(depth)),
		}
	}

	// Active windows
	windowKeys, _ := m.scanKeys("epoch:*:*:window")
	overview.ActiveWindows = len(windowKeys)

	// Ready batches
	readyKeys, _ := m.scanKeys(kb.ProtocolState + ":" + kb.DataMarket + ":batch:ready:*")
	overview.ReadyBatches = len(readyKeys)

	// Finalization queue
	finQueueKey := kb.FinalizationQueue()
	if depth, err := m.redis.LLen(m.ctx, finQueueKey).Result(); err == nil {
		overview.FinalizationQueue = QueueStatus{
			Depth:  int(depth),
			Status: getQueueStatus(int(depth)),
		}
	}

	// Active workers
	workerKeys, _ := m.scanKeys(kb.ProtocolState + ":" + kb.DataMarket + ":worker:*:status")
	overview.ActiveWorkers = len(workerKeys)

	// Completed parts
	partKeys, _ := m.scanKeys(kb.ProtocolState + ":" + kb.DataMarket + ":batch:part:*")
	overview.CompletedParts = len(partKeys)

	// Finalized batches
	finalizedKeys, _ := m.scanKeys(kb.ProtocolState + ":" + kb.DataMarket + ":finalized:*")
	overview.FinalizedBatches = len(finalizedKeys)

	// Aggregation queue
	aggQueueKey := kb.AggregationQueue()
	if depth, err := m.redis.LLen(m.ctx, aggQueueKey).Result(); err == nil {
		overview.AggregationQueue = QueueStatus{
			Depth:  int(depth),
			Status: getQueueStatus(int(depth)),
		}
	}

	// Aggregated batches
	aggKeys, _ := m.scanKeys(kb.ProtocolState + ":" + kb.DataMarket + ":batch:aggregated:*")
	overview.AggregatedBatches = len(aggKeys)

	c.JSON(http.StatusOK, overview)
}

// @Summary Submission windows
// @Description Get active and recently closed submission windows
// @Tags submissions
// @Produce json
// @Success 200 {object} []SubmissionWindow
// @Router /submissions/windows [get]
func (m *MonitorAPI) SubmissionWindows(c *gin.Context) {
	windowKeys, err := m.scanKeys("epoch:*:*:window")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	windows := make([]SubmissionWindow, 0)
	for _, key := range windowKeys {
		status, _ := m.redis.Get(m.ctx, key).Result()
		ttl, _ := m.redis.TTL(m.ctx, key).Result()

		window := SubmissionWindow{
			EpochID: key,
			Status:  status,
			TTL:     int64(ttl.Seconds()),
		}

		if status == "closed" {
			window.ClosedAt = time.Now().Add(-time.Hour + ttl)
		}

		windows = append(windows, window)
	}

	c.JSON(http.StatusOK, windows)
}

// @Summary Submission queue status
// @Description Get submission queue depth and sample entries
// @Tags submissions
// @Produce json
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Success 200 {object} map[string]interface{}
// @Router /submissions/queue [get]
func (m *MonitorAPI) SubmissionQueue(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	queueKey := kb.SubmissionQueue()
	depth, _ := m.redis.LLen(m.ctx, queueKey).Result()
	samples, _ := m.redis.LRange(m.ctx, queueKey, 0, 4).Result()

	c.JSON(http.StatusOK, gin.H{
		"queue_key": queueKey,
		"depth":     depth,
		"status":    getQueueStatus(int(depth)),
		"samples":   samples,
	})
}

// @Summary Ready batches
// @Description Get batches ready for finalization
// @Tags batches
// @Produce json
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Success 200 {object} []ReadyBatch
// @Router /batches/ready [get]
func (m *MonitorAPI) ReadyBatches(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	pattern := kb.ProtocolState + ":" + kb.DataMarket + ":batch:ready:*"
	keys, err := m.scanKeys(pattern)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	batches := make([]ReadyBatch, 0)
	for _, key := range keys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var metaData map[string]interface{}
		json.Unmarshal([]byte(data), &metaData)

		batch := ReadyBatch{
			EpochID:      key,
			ProjectCount: len(metaData),
			HasVotes:     containsVotes(metaData),
		}
		batches = append(batches, batch)
	}

	c.JSON(http.StatusOK, batches)
}

// @Summary Finalization queue
// @Description Get finalization queue status
// @Tags batches
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /batches/finalization-queue [get]
func (m *MonitorAPI) FinalizationQueue(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	queueKey := kb.FinalizationQueue()
	depth, _ := m.redis.LLen(m.ctx, queueKey).Result()

	samples, _ := m.redis.LRange(m.ctx, queueKey, 0, 4).Result()

	c.JSON(http.StatusOK, gin.H{
		"queue_key": queueKey,
		"depth":     depth,
		"status":    getQueueStatus(int(depth)),
		"samples":   samples,
	})
}

// @Summary Worker status
// @Description Get status of all finalization workers
// @Tags workers
// @Produce json
// @Success 200 {object} []WorkerStatus
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /workers/status [get]
func (m *MonitorAPI) WorkerStatus(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	pattern := kb.ProtocolState + ":" + kb.DataMarket + ":worker:*:status"
	keys, err := m.scanKeys(pattern)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	workers := make([]WorkerStatus, 0)
	for _, key := range keys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var statusData map[string]interface{}
		json.Unmarshal([]byte(data), &statusData)

		heartbeatKey := key + ":heartbeat"
		heartbeatStr, _ := m.redis.Get(m.ctx, heartbeatKey).Result()
		heartbeat, _ := strconv.ParseInt(heartbeatStr, 10, 64)
		lastHeartbeat := time.Unix(heartbeat, 0)

		worker := WorkerStatus{
			WorkerID:      key,
			Status:        fmt.Sprintf("%v", statusData["status"]),
			LastHeartbeat: lastHeartbeat,
			Healthy:       time.Since(lastHeartbeat) < 60*time.Second,
		}

		if epochID, ok := statusData["epoch_id"]; ok {
			worker.EpochID = fmt.Sprintf("%v", epochID)
		}
		if projectID, ok := statusData["project_id"]; ok {
			worker.ProjectID = fmt.Sprintf("%v", projectID)
		}

		workers = append(workers, worker)
	}

	c.JSON(http.StatusOK, workers)
}

// @Summary Batch parts
// @Description Get completed batch parts from parallel finalization
// @Tags batches
// @Produce json
// @Success 200 {object} []BatchPart
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /batches/parts [get]
func (m *MonitorAPI) BatchParts(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	pattern := kb.ProtocolState + ":" + kb.DataMarket + ":batch:part:*"
	keys, err := m.scanKeys(pattern)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	parts := make([]BatchPart, 0)
	for _, key := range keys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var partData map[string]interface{}
		json.Unmarshal([]byte(data), &partData)

		part := BatchPart{
			EpochID:      key,
			ProjectCount: len(partData),
		}
		parts = append(parts, part)
	}

	c.JSON(http.StatusOK, parts)
}

// @Summary Finalized batches
// @Description Get finalized batches with details
// @Tags batches
// @Produce json
// @Success 200 {object} []FinalizedBatch
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /batches/finalized [get]
func (m *MonitorAPI) FinalizedBatches(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	pattern := kb.ProtocolState + ":" + kb.DataMarket + ":finalized:*"
	keys, err := m.scanKeys(pattern)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	batches := make([]FinalizedBatch, 0)
	for _, key := range keys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		batch := FinalizedBatch{
			EpochID:      fmt.Sprintf("%v", batchData["EpochId"]),
			SequencerID:  fmt.Sprintf("%v", batchData["SequencerId"]),
		}

		if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
			batch.ProjectCount = len(projectIds)
		}

		if votes, ok := batchData["ProjectVotes"].(map[string]interface{}); ok {
			batch.VoteCount = len(votes)
		}

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batch.Timestamp = time.Unix(int64(ts), 0)
		}

		batches = append(batches, batch)
	}

	c.JSON(http.StatusOK, batches)
}

// @Summary Aggregation queue
// @Description Get aggregation queue status
// @Tags aggregation
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /aggregation/queue [get]
func (m *MonitorAPI) AggregationQueue(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	queueKey := kb.AggregationQueue()
	depth, _ := m.redis.LLen(m.ctx, queueKey).Result()

	samples, _ := m.redis.LRange(m.ctx, queueKey, 0, 4).Result()

	c.JSON(http.StatusOK, gin.H{
		"queue_key": queueKey,
		"depth":     depth,
		"status":    getQueueStatus(int(depth)),
		"samples":   samples,
	})
}

// @Summary Aggregated batches
// @Description Get network-wide aggregated batches
// @Tags aggregation
// @Produce json
// @Success 200 {object} []AggregatedBatch
// @Param protocol query string false "Protocol state identifier"
// @Param market query string false "Data market address"
// @Router /aggregation/results [get]
func (m *MonitorAPI) AggregatedBatches(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", m.keyBuilder.ProtocolState)
	market := c.DefaultQuery("market", m.keyBuilder.DataMarket)
	kb := keys.NewKeyBuilder(protocol, market)

	pattern := kb.ProtocolState + ":" + kb.DataMarket + ":batch:aggregated:*"
	keys, err := m.scanKeys(pattern)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	batches := make([]AggregatedBatch, 0)
	for _, key := range keys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		batch := AggregatedBatch{
			EpochID: fmt.Sprintf("%v", batchData["EpochId"]),
		}

		if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
			batch.ProjectCount = len(projectIds)
		}

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batch.Timestamp = time.Unix(int64(ts), 0)
		}

		batches = append(batches, batch)
	}

	c.JSON(http.StatusOK, batches)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getQueueStatus(depth int) string {
	if depth == 0 {
		return "empty"
	} else if depth < 10 {
		return "healthy"
	} else if depth < 50 {
		return "busy"
	}
	return "backlog"
}

func containsVotes(data map[string]interface{}) bool {
	for _, v := range data {
		if dataMap, ok := v.(map[string]interface{}); ok {
			if _, exists := dataMap["cid_votes"]; exists {
				return true
			}
		}
	}
	return false
}

// scanKeys uses SCAN instead of KEYS for production safety
func (m *MonitorAPI) scanKeys(pattern string) ([]string, error) {
	var keys []string
	var cursor uint64

	for {
		var scanKeys []string
		var err error

		scanKeys, cursor, err = m.redis.Scan(m.ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}

		keys = append(keys, scanKeys...)

		if cursor == 0 {
			break
		}
	}

	return keys, nil
}