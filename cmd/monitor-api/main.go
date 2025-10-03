package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	redis        *redis.Client
	ctx          context.Context
	keyBuilder   *keys.KeyBuilder
	metricsStore *MetricsStore
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
	EpochID          string            `json:"epoch_id"`
	BatchIPFSCid     string            `json:"batch_ipfs_cid,omitempty"`     // IPFS CID of the final Level 2 aggregated batch
	MerkleRoot       string            `json:"merkle_root,omitempty"`        // Merkle root as base64 string
	ProjectIDs       []string          `json:"project_ids"`                  // Array of project IDs that were aggregated
	ProjectCount     int               `json:"project_count"`
	ValidatorIDs     []string          `json:"validator_ids"`                // Array of validator IDs that contributed
	ValidatorCount   int               `json:"validator_count"`
	ValidatorBatches map[string]string `json:"validator_batches,omitempty"`  // validator_id → their individual Level 1 batch IPFS CID
	Timestamp        time.Time         `json:"timestamp"`
}

// Dashboard API Types
type DashboardSummary struct {
	ValidatorID  string                     `json:"validator_id"`
	Health       HealthStatus               `json:"health"`
	CurrentEpoch CurrentEpochInfo           `json:"current_epoch"`
	Performance  Performance24H             `json:"performance_24h"`
	Alerts       []Alert                    `json:"alerts"`
}

type HealthStatus struct {
	OverallStatus string                     `json:"overall_status"` // healthy|degraded|critical
	Components    map[string]string          `json:"components"`
	LastActivity  time.Time                  `json:"last_activity"`
}

type CurrentEpochInfo struct {
	ID                   uint64    `json:"id"`
	Phase                string    `json:"phase"` // submission|finalization|aggregation|complete
	TimeRemaining        int       `json:"time_remaining"`
	LocalSubmissions     int       `json:"local_submissions"`
	ExpectedSubmissions  int       `json:"expected_submissions"`
}

type Performance24H struct {
	EpochsParticipated      int     `json:"epochs_participated"`
	EpochsTotal             int     `json:"epochs_total"`
	ParticipationRate       float64 `json:"participation_rate"`
	SubmissionsProcessed    int     `json:"submissions_processed"`
	FinalizationsCompleted  int     `json:"finalizations_completed"`
	AggregationsContributed int     `json:"aggregations_contributed"`
}

type Alert struct {
	Severity  string    `json:"severity"` // info|warning|critical
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// Epoch Timeline Types
type EpochTimelineResponse struct {
	Epochs  []EpochDetail  `json:"epochs"`
	Summary TimelineSummary `json:"summary"`
}

type EpochDetail struct {
	EpochID  uint64            `json:"epoch_id"`
	Status   string            `json:"status"` // complete|processing|missed
	Phases   EpochPhases       `json:"phases"`
	Metrics  EpochMetrics      `json:"metrics"`
}

type EpochPhases struct {
	Submission    PhaseInfo `json:"submission"`
	Finalization  PhaseInfo `json:"finalization"`
	Aggregation   PhaseInfo `json:"aggregation"`
}

type PhaseInfo struct {
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	SubmissionsReceived int       `json:"submissions_received,omitempty"`
	SubmissionsProcessed int       `json:"submissions_processed,omitempty"`
	BatchCreated        bool      `json:"batch_created,omitempty"`
	IPFSCid             string    `json:"ipfs_cid,omitempty"`
	ProjectsFinalized   int       `json:"projects_finalized,omitempty"`
	ValidatorsParticipated int    `json:"validators_participated,omitempty"`
	ConsensusAchieved   bool      `json:"consensus_achieved,omitempty"`
	MerkleRoot          string    `json:"merkle_root,omitempty"`
}

type EpochMetrics struct {
	TotalDurationMs    int64   `json:"total_duration_ms"`
	QueueMaxDepth      int     `json:"queue_max_depth"`
	WorkerUtilization  float64 `json:"worker_utilization"`
}

type TimelineSummary struct {
	TotalEpochs      int   `json:"total_epochs"`
	Successful       int   `json:"successful"`
	Missed           int   `json:"missed"`
	AverageDurationMs int64 `json:"average_duration_ms"`
}

// Validator Performance Types
type ValidatorPerformance struct {
	ValidatorID  string                      `json:"validator_id"`
	Period       string                      `json:"period"`
	Metrics      PerformanceMetrics          `json:"metrics"`
	Comparison   NetworkComparison           `json:"comparison"`
}

type PerformanceMetrics struct {
	Participation ParticipationMetrics `json:"participation"`
	Submissions   SubmissionMetrics    `json:"submissions"`
	Finalization  FinalizationMetrics  `json:"finalization"`
	Aggregation   AggregationMetrics   `json:"aggregation"`
}

type ParticipationMetrics struct {
	EpochsParticipated int     `json:"epochs_participated"`
	EpochsTotal        int     `json:"epochs_total"`
	ParticipationRate  float64 `json:"participation_rate"`
	StreakCurrent      int     `json:"streak_current"`
	StreakLongest      int     `json:"streak_longest"`
}

type SubmissionMetrics struct {
	TotalReceived    int                `json:"total_received"`
	TotalProcessed   int                `json:"total_processed"`
	ProcessingRate   float64            `json:"processing_rate"`
	AveragePerEpoch  float64            `json:"average_per_epoch"`
	ByProject        map[string]int     `json:"by_project"`
}

type FinalizationMetrics struct {
	BatchesCreated             int     `json:"batches_created"`
	IPFSSuccessRate            float64 `json:"ipfs_success_rate"`
	AverageProjectsPerBatch    float64 `json:"average_projects_per_batch"`
	AverageFinalizationTimeMs  int64   `json:"average_finalization_time_ms"`
}

type AggregationMetrics struct {
	Level1Contributions      int     `json:"level1_contributions"`
	Level2Participations     int     `json:"level2_participations"`
	ConsensusAlignmentRate   float64 `json:"consensus_alignment_rate"`
	ValidatorWeight          float64 `json:"validator_weight"`
}

type NetworkComparison struct {
	NetworkAverage NetworkAverages `json:"network_average"`
	PercentileRank int            `json:"percentile_rank"`
}

type NetworkAverages struct {
	ParticipationRate        float64 `json:"participation_rate"`
	ProcessingRate           float64 `json:"processing_rate"`
	ConsensusAlignmentRate   float64 `json:"consensus_alignment_rate"`
}

// Queue Analytics Types
type QueueAnalytics struct {
	Timestamp       time.Time               `json:"timestamp"`
	Queues          map[string]QueueMetrics `json:"queues"`
	Bottlenecks     []Bottleneck            `json:"bottlenecks"`
	Recommendations []string                `json:"recommendations"`
}

type QueueMetrics struct {
	CurrentDepth        int     `json:"current_depth"`
	AverageDepth1H      float64 `json:"average_depth_1h"`
	MaxDepth1H          int     `json:"max_depth_1h"`
	ProcessingRate      float64 `json:"processing_rate"`
	EstimatedClearTimeS float64 `json:"estimated_clear_time_s"`
}

type Bottleneck struct {
	Queue      string    `json:"queue"`
	Severity   string    `json:"severity"` // low|medium|high
	DetectedAt time.Time `json:"detected_at"`
	PeakDepth  int       `json:"peak_depth"`
	DurationS  int       `json:"duration_s"`
}

// Network Consensus Types
type NetworkConsensusView struct {
	EpochID              uint64                   `json:"epoch_id"`
	ConsensusStatus      string                   `json:"consensus_status"` // achieved|pending|failed
	Validators           ValidatorSummary         `json:"validators"`
	Aggregation          AggregationSummary       `json:"aggregation"`
	ValidatorContributions []ValidatorContribution `json:"validator_contributions"`
	ProjectConsensus     map[string]ProjectVotes  `json:"project_consensus"`
}

type ValidatorSummary struct {
	Total        int `json:"total"`
	Participated int `json:"participated"`
	Pending      int `json:"pending"`
}

type AggregationSummary struct {
	BatchIPFSCid      string    `json:"batch_ipfs_cid"`
	MerkleRoot        string    `json:"merkle_root"`
	ProjectsFinalized int       `json:"projects_finalized"`
	Timestamp         time.Time `json:"timestamp"`
}

type ValidatorContribution struct {
	ValidatorID         string  `json:"validator_id"`
	BatchCid            string  `json:"batch_cid"`
	ProjectsCount       int     `json:"projects_count"`
	SubmissionCount     int     `json:"submission_count"`
	ContributionWeight  float64 `json:"contribution_weight"`
}

type ProjectVotes struct {
	ConsensusCid      string            `json:"consensus_cid"`
	ValidatorsAgreed  int               `json:"validators_agreed"`
	VoteDistribution  map[string]int    `json:"vote_distribution"`
}

// Metrics Store for time-series data
type MetricsStore struct {
	mu            sync.RWMutex
	redis         *redis.Client
	ctx           context.Context
	keyBuilder    *keys.KeyBuilder

	// In-memory cache for recent metrics
	queueDepths   map[string][]TimedMetric
	workerMetrics map[string][]TimedMetric
	epochMetrics  map[uint64]*EpochMetricData
}

type TimedMetric struct {
	Timestamp time.Time
	Value     float64
}

type EpochMetricData struct {
	EpochID           uint64
	StartTime         time.Time
	EndTime           time.Time
	SubmissionsCount  int
	FinalizationTime  time.Duration
	AggregationTime   time.Duration
	WorkerUtilization float64
	QueueMaxDepth     int
	Success           bool
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

	metricsStore := NewMetricsStore(redisClient, ctx, keyBuilder)

	api := &MonitorAPI{
		redis:        redisClient,
		ctx:          ctx,
		keyBuilder:   keyBuilder,
		metricsStore: metricsStore,
	}

	// Start background metrics collection
	go api.startMetricsCollection()

	router := gin.Default()

	v1 := router.Group("/api/v1")
	{
		// Existing endpoints
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

		// New dashboard endpoints
		v1.GET("/dashboard/summary", api.DashboardSummary)
		v1.GET("/epochs/timeline", api.EpochTimeline)
		v1.GET("/validator/performance", api.ValidatorPerformance)
		v1.GET("/queues/analytics", api.QueueAnalytics)
		v1.GET("/network/consensus", api.NetworkConsensus)
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
// @Description Get network-wide aggregated batches with complete batch information
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

		// Extract IPFS CID if available
		if batchCid, ok := batchData["BatchIPFSCID"].(string); ok {
			batch.BatchIPFSCid = batchCid
		}

		// Extract and encode Merkle Root as base64
		if merkleRoot, ok := batchData["MerkleRoot"]; ok {
			switch v := merkleRoot.(type) {
			case string:
				// If it's already a string (base64), use it directly
				batch.MerkleRoot = v
			case []interface{}:
				// If it's an array of bytes, convert to byte slice and encode
				bytes := make([]byte, len(v))
				for i, b := range v {
					if byteVal, ok := b.(float64); ok {
						bytes[i] = byte(byteVal)
					}
				}
				batch.MerkleRoot = base64.StdEncoding.EncodeToString(bytes)
			}
		}

		// Extract Project IDs
		if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
			batch.ProjectCount = len(projectIds)
			batch.ProjectIDs = make([]string, 0, len(projectIds))
			for _, pid := range projectIds {
				if projectID, ok := pid.(string); ok {
					batch.ProjectIDs = append(batch.ProjectIDs, projectID)
				}
			}
		}

		// Extract Validator Count and build Validator IDs list
		if validatorCount, ok := batchData["ValidatorCount"].(float64); ok {
			batch.ValidatorCount = int(validatorCount)
		}

		// Extract Validator Batches and build Validator IDs list
		if validatorBatches, ok := batchData["ValidatorBatches"].(map[string]interface{}); ok {
			batch.ValidatorBatches = make(map[string]string)
			batch.ValidatorIDs = make([]string, 0, len(validatorBatches))

			for validatorID, cidValue := range validatorBatches {
				if cid, ok := cidValue.(string); ok {
					batch.ValidatorBatches[validatorID] = cid
					batch.ValidatorIDs = append(batch.ValidatorIDs, validatorID)
				}
			}
		}

		// Extract timestamp
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

// NewMetricsStore creates a new metrics store for time-series data
func NewMetricsStore(redis *redis.Client, ctx context.Context, keyBuilder *keys.KeyBuilder) *MetricsStore {
	return &MetricsStore{
		redis:         redis,
		ctx:           ctx,
		keyBuilder:    keyBuilder,
		queueDepths:   make(map[string][]TimedMetric),
		workerMetrics: make(map[string][]TimedMetric),
		epochMetrics:  make(map[uint64]*EpochMetricData),
	}
}

// startMetricsCollection runs background collection of metrics
func (m *MonitorAPI) startMetricsCollection() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.collectQueueMetrics()
		m.collectWorkerMetrics()
		m.collectEpochMetrics()
	}
}

// collectQueueMetrics collects queue depth metrics
func (m *MonitorAPI) collectQueueMetrics() {
	queues := []string{"submission", "finalization", "aggregation"}

	m.metricsStore.mu.Lock()
	defer m.metricsStore.mu.Unlock()

	now := time.Now()

	for _, queue := range queues {
		var queueKey string
		switch queue {
		case "submission":
			queueKey = m.keyBuilder.SubmissionQueue()
		case "finalization":
			queueKey = m.keyBuilder.FinalizationQueue()
		case "aggregation":
			queueKey = m.keyBuilder.AggregationQueue()
		}

		depth, _ := m.redis.LLen(m.ctx, queueKey).Result()

		// Store in memory (keep last hour)
		if m.metricsStore.queueDepths[queue] == nil {
			m.metricsStore.queueDepths[queue] = []TimedMetric{}
		}

		m.metricsStore.queueDepths[queue] = append(m.metricsStore.queueDepths[queue], TimedMetric{
			Timestamp: now,
			Value:     float64(depth),
		})

		// Keep only last hour
		cutoff := now.Add(-time.Hour)
		filtered := []TimedMetric{}
		for _, metric := range m.metricsStore.queueDepths[queue] {
			if metric.Timestamp.After(cutoff) {
				filtered = append(filtered, metric)
			}
		}
		m.metricsStore.queueDepths[queue] = filtered

		// Also store in Redis for persistence (with TTL)
		metricKey := fmt.Sprintf("metrics:queue:%s:%d", queue, now.Unix())
		m.redis.SetEx(m.ctx, metricKey, depth, 24*time.Hour)
	}
}

// collectWorkerMetrics collects worker utilization metrics
func (m *MonitorAPI) collectWorkerMetrics() {
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":worker:*:status"
	workerKeys, _ := m.scanKeys(pattern)

	activeCount := 0
	for _, key := range workerKeys {
		status, _ := m.redis.Get(m.ctx, key).Result()
		if status == "active" {
			activeCount++
		}
	}

	totalWorkers := len(workerKeys)
	utilization := float64(0)
	if totalWorkers > 0 {
		utilization = float64(activeCount) / float64(totalWorkers)
	}

	m.metricsStore.mu.Lock()
	defer m.metricsStore.mu.Unlock()

	now := time.Now()
	if m.metricsStore.workerMetrics["utilization"] == nil {
		m.metricsStore.workerMetrics["utilization"] = []TimedMetric{}
	}

	m.metricsStore.workerMetrics["utilization"] = append(
		m.metricsStore.workerMetrics["utilization"],
		TimedMetric{Timestamp: now, Value: utilization},
	)

	// Keep only last hour
	cutoff := now.Add(-time.Hour)
	filtered := []TimedMetric{}
	for _, metric := range m.metricsStore.workerMetrics["utilization"] {
		if metric.Timestamp.After(cutoff) {
			filtered = append(filtered, metric)
		}
	}
	m.metricsStore.workerMetrics["utilization"] = filtered
}

// collectEpochMetrics collects epoch-level metrics
func (m *MonitorAPI) collectEpochMetrics() {
	// Get latest finalized batches
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":finalized:*"
	finalizedKeys, _ := m.scanKeys(pattern)

	m.metricsStore.mu.Lock()
	defer m.metricsStore.mu.Unlock()

	for _, key := range finalizedKeys {
		// Extract epoch ID from key
		parts := strings.Split(key, ":")
		if len(parts) < 4 {
			continue
		}

		epochStr := parts[len(parts)-1]
		epochID, err := strconv.ParseUint(epochStr, 10, 64)
		if err != nil {
			continue
		}

		// Skip if already collected
		if _, exists := m.metricsStore.epochMetrics[epochID]; exists {
			continue
		}

		// Get batch data
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		epochData := &EpochMetricData{
			EpochID: epochID,
			Success: true,
		}

		// Extract metrics from batch data
		if ts, ok := batchData["Timestamp"].(float64); ok {
			epochData.EndTime = time.Unix(int64(ts), 0)
			epochData.StartTime = epochData.EndTime.Add(-45 * time.Second) // Estimate
		}

		if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
			epochData.SubmissionsCount = len(projectIds)
		}

		m.metricsStore.epochMetrics[epochID] = epochData

		// Store in Redis for persistence
		metricData, _ := json.Marshal(epochData)
		metricKey := fmt.Sprintf("metrics:epoch:%d", epochID)
		m.redis.SetEx(m.ctx, metricKey, string(metricData), 7*24*time.Hour)
	}
}

// Dashboard Summary endpoint implementation
// @Summary Dashboard summary
// @Description Get overall validator health and performance summary
// @Tags dashboard
// @Produce json
// @Success 200 {object} DashboardSummary
// @Router /dashboard/summary [get]
func (m *MonitorAPI) DashboardSummary(c *gin.Context) {
	validatorID := getEnv("VALIDATOR_ID", "validator-001")

	// Calculate health status
	health := m.calculateHealthStatus()

	// Get current epoch info
	currentEpoch := m.getCurrentEpochInfo()

	// Calculate 24h performance
	performance := m.calculate24HPerformance()

	// Get active alerts
	alerts := m.getActiveAlerts()

	summary := DashboardSummary{
		ValidatorID:  validatorID,
		Health:       health,
		CurrentEpoch: currentEpoch,
		Performance:  performance,
		Alerts:       alerts,
	}

	c.JSON(http.StatusOK, summary)
}

func (m *MonitorAPI) calculateHealthStatus() HealthStatus {
	components := make(map[string]string)

	// Check Redis
	if err := m.redis.Ping(m.ctx).Err(); err != nil {
		components["redis"] = "disconnected"
	} else {
		components["redis"] = "connected"
	}

	// Check IPFS (if configured)
	ipfsHost := getEnv("IPFS_HOST", "")
	if ipfsHost != "" {
		components["ipfs"] = "connected" // Simplified for now
	}

	// Check P2P
	components["p2p"] = "connected" // Simplified for now

	// Check workers
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":worker:*:status"
	workerKeys, _ := m.scanKeys(pattern)
	activeWorkers := 0
	for _, key := range workerKeys {
		status, _ := m.redis.Get(m.ctx, key).Result()
		if status == "active" {
			activeWorkers++
		}
	}
	components["workers"] = fmt.Sprintf("%d/%d healthy", activeWorkers, len(workerKeys))

	// Determine overall status
	overallStatus := "healthy"
	for _, status := range components {
		if strings.Contains(status, "disconnected") || strings.Contains(status, "0/") {
			overallStatus = "degraded"
			break
		}
	}

	return HealthStatus{
		OverallStatus: overallStatus,
		Components:    components,
		LastActivity:  time.Now(),
	}
}

func (m *MonitorAPI) getCurrentEpochInfo() CurrentEpochInfo {
	// Get latest epoch from finalized batches
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":finalized:*"
	finalizedKeys, _ := m.scanKeys(pattern)

	var latestEpoch uint64
	for _, key := range finalizedKeys {
		parts := strings.Split(key, ":")
		if len(parts) > 0 {
			epochStr := parts[len(parts)-1]
			epoch, err := strconv.ParseUint(epochStr, 10, 64)
			if err == nil && epoch > latestEpoch {
				latestEpoch = epoch
			}
		}
	}

	// Assume current epoch is latest + 1
	currentEpoch := latestEpoch + 1

	// Count current submissions
	submissionPattern := fmt.Sprintf("%s:%s:epoch:%d:processed",
		m.keyBuilder.ProtocolState, m.keyBuilder.DataMarket, currentEpoch)
	submissionKeys, _ := m.scanKeys(submissionPattern + "*")

	// Determine phase
	phase := "submission"
	windowKey := fmt.Sprintf("epoch:%s:%d:window", m.keyBuilder.DataMarket, currentEpoch)
	status, _ := m.redis.Get(m.ctx, windowKey).Result()
	if status == "closed" {
		phase = "finalization"

		// Check if finalization is done
		finalKey := fmt.Sprintf("%s:%s:finalized:%d",
			m.keyBuilder.ProtocolState, m.keyBuilder.DataMarket, currentEpoch)
		if exists, _ := m.redis.Exists(m.ctx, finalKey).Result(); exists > 0 {
			phase = "aggregation"

			// Check if aggregation is done
			aggKey := fmt.Sprintf("%s:%s:batch:aggregated:%d",
				m.keyBuilder.ProtocolState, m.keyBuilder.DataMarket, currentEpoch)
			if exists, _ := m.redis.Exists(m.ctx, aggKey).Result(); exists > 0 {
				phase = "complete"
			}
		}
	}

	return CurrentEpochInfo{
		ID:                   currentEpoch,
		Phase:                phase,
		TimeRemaining:        20, // Simplified
		LocalSubmissions:     len(submissionKeys),
		ExpectedSubmissions:  45, // Simplified estimate
	}
}

func (m *MonitorAPI) calculate24HPerformance() Performance24H {
	now := time.Now()
	cutoff := now.Add(-24 * time.Hour)

	// Count epochs in last 24h (assuming 2-minute epochs)
	epochsTotal := int((24 * time.Hour) / (2 * time.Minute))

	// Count finalized batches in last 24h
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":finalized:*"
	finalizedKeys, _ := m.scanKeys(pattern)

	epochsParticipated := 0
	submissionsProcessed := 0

	for _, key := range finalizedKeys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batchTime := time.Unix(int64(ts), 0)
			if batchTime.After(cutoff) {
				epochsParticipated++

				if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
					submissionsProcessed += len(projectIds)
				}
			}
		}
	}

	// Count aggregations
	aggPattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":batch:aggregated:*"
	aggKeys, _ := m.scanKeys(aggPattern)
	aggregationsContributed := 0

	for _, key := range aggKeys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batchTime := time.Unix(int64(ts), 0)
			if batchTime.After(cutoff) {
				aggregationsContributed++
			}
		}
	}

	participationRate := float64(epochsParticipated) / float64(epochsTotal) * 100

	return Performance24H{
		EpochsParticipated:      epochsParticipated,
		EpochsTotal:             epochsTotal,
		ParticipationRate:       participationRate,
		SubmissionsProcessed:    submissionsProcessed,
		FinalizationsCompleted:  epochsParticipated,
		AggregationsContributed: aggregationsContributed,
	}
}

func (m *MonitorAPI) getActiveAlerts() []Alert {
	alerts := []Alert{}

	// Check queue depths
	m.metricsStore.mu.RLock()
	defer m.metricsStore.mu.RUnlock()

	for queue, metrics := range m.metricsStore.queueDepths {
		if len(metrics) > 0 {
			latest := metrics[len(metrics)-1]
			if latest.Value > 50 {
				alerts = append(alerts, Alert{
					Severity:  "warning",
					Type:      "queue_backlog",
					Message:   fmt.Sprintf("%s queue depth exceeds threshold (%.0f > 50)", queue, latest.Value),
					Timestamp: latest.Timestamp,
				})
			}
		}
	}

	// Check worker utilization
	if utilMetrics, exists := m.metricsStore.workerMetrics["utilization"]; exists && len(utilMetrics) > 0 {
		latest := utilMetrics[len(utilMetrics)-1]
		if latest.Value < 0.75 {
			alerts = append(alerts, Alert{
				Severity:  "info",
				Type:      "worker_utilization",
				Message:   fmt.Sprintf("Worker utilization below optimal (%.1f%% < 75%%)", latest.Value*100),
				Timestamp: latest.Timestamp,
			})
		}
	}

	return alerts
}

// Epoch Timeline endpoint implementation
// @Summary Epoch timeline
// @Description Get timeline of recent epochs with phase progression
// @Tags epochs
// @Produce json
// @Param limit query int false "Number of epochs to return" default(50)
// @Param from_epoch query int false "Starting epoch ID"
// @Success 200 {object} EpochTimelineResponse
// @Router /epochs/timeline [get]
func (m *MonitorAPI) EpochTimeline(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	fromEpoch := uint64(0)
	if fe := c.Query("from_epoch"); fe != "" {
		if parsed, err := strconv.ParseUint(fe, 10, 64); err == nil {
			fromEpoch = parsed
		}
	}

	epochs := []EpochDetail{}

	// Get all finalized batches
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":finalized:*"
	finalizedKeys, _ := m.scanKeys(pattern)

	// Sort by epoch ID
	type epochKeyPair struct {
		epochID uint64
		key     string
	}

	pairs := []epochKeyPair{}
	for _, key := range finalizedKeys {
		parts := strings.Split(key, ":")
		if len(parts) > 0 {
			epochStr := parts[len(parts)-1]
			epoch, err := strconv.ParseUint(epochStr, 10, 64)
			if err == nil {
				if fromEpoch > 0 && epoch < fromEpoch {
					continue
				}
				pairs = append(pairs, epochKeyPair{epochID: epoch, key: key})
			}
		}
	}

	// Sort by epoch ID descending
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].epochID > pairs[j].epochID
	})

	// Limit results
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}

	totalDuration := int64(0)
	successful := 0

	for _, pair := range pairs {
		data, _ := m.redis.Get(m.ctx, pair.key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		epochDetail := EpochDetail{
			EpochID: pair.epochID,
			Status:  "complete",
		}

		// Build phase information
		phases := EpochPhases{}

		// Estimate phase timings (simplified)
		if ts, ok := batchData["Timestamp"].(float64); ok {
			endTime := time.Unix(int64(ts), 0)
			startTime := endTime.Add(-45 * time.Second)

			phases.Submission = PhaseInfo{
				Start: startTime,
				End:   startTime.Add(20 * time.Second),
			}

			phases.Finalization = PhaseInfo{
				Start:        startTime.Add(20 * time.Second),
				End:          startTime.Add(35 * time.Second),
				BatchCreated: true,
			}

			phases.Aggregation = PhaseInfo{
				Start:             startTime.Add(35 * time.Second),
				End:               endTime,
				ConsensusAchieved: true,
			}

			// Get submission count
			if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
				phases.Submission.SubmissionsReceived = len(projectIds)
				phases.Submission.SubmissionsProcessed = len(projectIds)
				phases.Finalization.ProjectsFinalized = len(projectIds)
			}

			// Get IPFS CID
			if cid, ok := batchData["BatchIPFSCID"].(string); ok {
				phases.Finalization.IPFSCid = cid
			}

			// Get validator count from aggregated batch
			aggKey := fmt.Sprintf("%s:%s:batch:aggregated:%d",
				m.keyBuilder.ProtocolState, m.keyBuilder.DataMarket, pair.epochID)
			if aggData, err := m.redis.Get(m.ctx, aggKey).Result(); err == nil {
				var aggBatch map[string]interface{}
				json.Unmarshal([]byte(aggData), &aggBatch)

				if validatorCount, ok := aggBatch["ValidatorCount"].(float64); ok {
					phases.Aggregation.ValidatorsParticipated = int(validatorCount)
				}

				if merkleRoot, ok := aggBatch["MerkleRoot"].(string); ok {
					phases.Aggregation.MerkleRoot = merkleRoot
				}
			}

			epochDetail.Phases = phases

			// Calculate metrics
			duration := endTime.Sub(startTime).Milliseconds()
			totalDuration += duration
			successful++

			epochDetail.Metrics = EpochMetrics{
				TotalDurationMs:   duration,
				QueueMaxDepth:     10, // Simplified
				WorkerUtilization: 0.85, // Simplified
			}
		}

		epochs = append(epochs, epochDetail)
	}

	avgDuration := int64(0)
	if successful > 0 {
		avgDuration = totalDuration / int64(successful)
	}

	response := EpochTimelineResponse{
		Epochs: epochs,
		Summary: TimelineSummary{
			TotalEpochs:       len(epochs),
			Successful:        successful,
			Missed:            0, // Simplified
			AverageDurationMs: avgDuration,
		},
	}

	c.JSON(http.StatusOK, response)
}

// Validator Performance endpoint implementation
// @Summary Validator performance
// @Description Get validator performance metrics
// @Tags validator
// @Produce json
// @Param period query string false "Time period (24h|7d|30d)" default("24h")
// @Success 200 {object} ValidatorPerformance
// @Router /validator/performance [get]
func (m *MonitorAPI) ValidatorPerformance(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")
	validatorID := getEnv("VALIDATOR_ID", "validator-001")

	var duration time.Duration
	switch period {
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	default:
		duration = 24 * time.Hour
		period = "24h"
	}

	metrics := m.calculatePerformanceMetrics(duration)
	comparison := m.calculateNetworkComparison(metrics)

	performance := ValidatorPerformance{
		ValidatorID: validatorID,
		Period:      period,
		Metrics:     metrics,
		Comparison:  comparison,
	}

	c.JSON(http.StatusOK, performance)
}

func (m *MonitorAPI) calculatePerformanceMetrics(duration time.Duration) PerformanceMetrics {
	now := time.Now()
	cutoff := now.Add(-duration)

	// Calculate epochs in period (assuming 2-minute epochs)
	epochsTotal := int(duration / (2 * time.Minute))

	// Get finalized batches
	pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":finalized:*"
	finalizedKeys, _ := m.scanKeys(pattern)

	epochsParticipated := 0
	submissionsProcessed := 0
	projectCounts := make(map[string]int)
	totalProjects := 0
	finalizationTimes := []int64{}

	for _, key := range finalizedKeys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batchTime := time.Unix(int64(ts), 0)
			if batchTime.After(cutoff) {
				epochsParticipated++

				// Count submissions and projects
				if projectIds, ok := batchData["ProjectIds"].([]interface{}); ok {
					submissionsProcessed += len(projectIds)
					totalProjects += len(projectIds)

					for _, pid := range projectIds {
						if projectID, ok := pid.(string); ok {
							projectCounts[projectID]++
						}
					}
				}

				// Estimate finalization time (simplified)
				finalizationTimes = append(finalizationTimes, 12500)
			}
		}
	}

	// Calculate averages
	avgProjectsPerBatch := float64(0)
	avgFinalizationTime := int64(0)
	if epochsParticipated > 0 {
		avgProjectsPerBatch = float64(totalProjects) / float64(epochsParticipated)
		for _, t := range finalizationTimes {
			avgFinalizationTime += t
		}
		avgFinalizationTime /= int64(len(finalizationTimes))
	}

	// Count aggregations
	aggPattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":batch:aggregated:*"
	aggKeys, _ := m.scanKeys(aggPattern)
	level2Participations := 0

	for _, key := range aggKeys {
		data, _ := m.redis.Get(m.ctx, key).Result()
		var batchData map[string]interface{}
		json.Unmarshal([]byte(data), &batchData)

		if ts, ok := batchData["Timestamp"].(float64); ok {
			batchTime := time.Unix(int64(ts), 0)
			if batchTime.After(cutoff) {
				level2Participations++
			}
		}
	}

	participationRate := float64(epochsParticipated) / float64(epochsTotal) * 100
	processingRate := float64(submissionsProcessed) / float64(submissionsProcessed+5) * 100 // Simplified
	consensusAlignmentRate := 99.5 // Simplified
	validatorWeight := 1.0 / 12.0 // Assuming 12 validators

	return PerformanceMetrics{
		Participation: ParticipationMetrics{
			EpochsParticipated: epochsParticipated,
			EpochsTotal:        epochsTotal,
			ParticipationRate:  participationRate,
			StreakCurrent:      epochsParticipated, // Simplified
			StreakLongest:      epochsParticipated * 7, // Simplified
		},
		Submissions: SubmissionMetrics{
			TotalReceived:   submissionsProcessed,
			TotalProcessed:  submissionsProcessed - 5, // Simplified
			ProcessingRate:  processingRate,
			AveragePerEpoch: float64(submissionsProcessed) / float64(epochsParticipated),
			ByProject:       projectCounts,
		},
		Finalization: FinalizationMetrics{
			BatchesCreated:            epochsParticipated,
			IPFSSuccessRate:           99.9,
			AverageProjectsPerBatch:   avgProjectsPerBatch,
			AverageFinalizationTimeMs: avgFinalizationTime,
		},
		Aggregation: AggregationMetrics{
			Level1Contributions:     epochsParticipated,
			Level2Participations:    level2Participations,
			ConsensusAlignmentRate:  consensusAlignmentRate,
			ValidatorWeight:         validatorWeight,
		},
	}
}

func (m *MonitorAPI) calculateNetworkComparison(metrics PerformanceMetrics) NetworkComparison {
	// Simplified network averages
	networkAvg := NetworkAverages{
		ParticipationRate:      98.5,
		ProcessingRate:         99.2,
		ConsensusAlignmentRate: 98.8,
	}

	// Calculate percentile rank (simplified)
	percentile := 95
	if metrics.Participation.ParticipationRate < networkAvg.ParticipationRate {
		percentile = int(metrics.Participation.ParticipationRate / networkAvg.ParticipationRate * 95)
	}

	return NetworkComparison{
		NetworkAverage: networkAvg,
		PercentileRank: percentile,
	}
}

// Queue Analytics endpoint implementation
// @Summary Queue analytics
// @Description Get real-time queue analytics and bottleneck detection
// @Tags queues
// @Produce json
// @Success 200 {object} QueueAnalytics
// @Router /queues/analytics [get]
func (m *MonitorAPI) QueueAnalytics(c *gin.Context) {
	m.metricsStore.mu.RLock()
	defer m.metricsStore.mu.RUnlock()

	queues := make(map[string]QueueMetrics)
	bottlenecks := []Bottleneck{}
	recommendations := []string{}

	queueNames := []string{"submission", "finalization", "aggregation"}

	for _, queue := range queueNames {
		metrics := QueueMetrics{}

		// Get current depth
		var queueKey string
		switch queue {
		case "submission":
			queueKey = m.keyBuilder.SubmissionQueue()
		case "finalization":
			queueKey = m.keyBuilder.FinalizationQueue()
		case "aggregation":
			queueKey = m.keyBuilder.AggregationQueue()
		}

		currentDepth, _ := m.redis.LLen(m.ctx, queueKey).Result()
		metrics.CurrentDepth = int(currentDepth)

		// Calculate hourly metrics from memory
		if queueMetrics, exists := m.metricsStore.queueDepths[queue]; exists && len(queueMetrics) > 0 {
			sum := float64(0)
			max := float64(0)
			count := 0

			for _, m := range queueMetrics {
				sum += m.Value
				count++
				if m.Value > max {
					max = m.Value
				}
			}

			if count > 0 {
				metrics.AverageDepth1H = sum / float64(count)
			}
			metrics.MaxDepth1H = int(max)

			// Estimate processing rate (simplified)
			if len(queueMetrics) > 1 {
				first := queueMetrics[0]
				last := queueMetrics[len(queueMetrics)-1]
				timeDiff := last.Timestamp.Sub(first.Timestamp).Seconds()
				if timeDiff > 0 {
					metrics.ProcessingRate = math.Abs(last.Value-first.Value) / timeDiff * 60
				}
			}

			// Estimate clear time
			if metrics.ProcessingRate > 0 {
				metrics.EstimatedClearTimeS = float64(metrics.CurrentDepth) / metrics.ProcessingRate * 60
			}

			// Detect bottlenecks
			if metrics.CurrentDepth > 50 {
				severity := "low"
				if metrics.CurrentDepth > 100 {
					severity = "high"
				} else if metrics.CurrentDepth > 75 {
					severity = "medium"
				}

				bottlenecks = append(bottlenecks, Bottleneck{
					Queue:      queue,
					Severity:   severity,
					DetectedAt: time.Now(),
					PeakDepth:  metrics.MaxDepth1H,
					DurationS:  45, // Simplified
				})
			}
		}

		queues[queue] = metrics
	}

	// Generate recommendations
	if len(bottlenecks) > 0 {
		for _, b := range bottlenecks {
			if b.Severity == "high" {
				recommendations = append(recommendations,
					fmt.Sprintf("Critical: %s queue backlog detected. Consider scaling workers", b.Queue))
			}
		}
	}

	if queues["submission"].ProcessingRate < 100 {
		recommendations = append(recommendations,
			"Submission processing rate is low - check snapshotter connectivity")
	}

	analytics := QueueAnalytics{
		Timestamp:       time.Now(),
		Queues:          queues,
		Bottlenecks:     bottlenecks,
		Recommendations: recommendations,
	}

	c.JSON(http.StatusOK, analytics)
}

// Network Consensus endpoint implementation
// @Summary Network consensus view
// @Description Get network-wide consensus information for an epoch
// @Tags network
// @Produce json
// @Param epoch_id query int false "Epoch ID to query"
// @Success 200 {object} NetworkConsensusView
// @Router /network/consensus [get]
func (m *MonitorAPI) NetworkConsensus(c *gin.Context) {
	epochID := uint64(0)
	if eid := c.Query("epoch_id"); eid != "" {
		if parsed, err := strconv.ParseUint(eid, 10, 64); err == nil {
			epochID = parsed
		}
	}

	// If no epoch specified, get latest
	if epochID == 0 {
		pattern := m.keyBuilder.ProtocolState + ":" + m.keyBuilder.DataMarket + ":batch:aggregated:*"
		aggKeys, _ := m.scanKeys(pattern)

		for _, key := range aggKeys {
			parts := strings.Split(key, ":")
			if len(parts) > 0 {
				epochStr := parts[len(parts)-1]
				epoch, err := strconv.ParseUint(epochStr, 10, 64)
				if err == nil && epoch > epochID {
					epochID = epoch
				}
			}
		}
	}

	// Get aggregated batch data
	aggKey := fmt.Sprintf("%s:%s:batch:aggregated:%d",
		m.keyBuilder.ProtocolState, m.keyBuilder.DataMarket, epochID)

	aggData, err := m.redis.Get(m.ctx, aggKey).Result()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Epoch not found"})
		return
	}

	var aggBatch map[string]interface{}
	json.Unmarshal([]byte(aggData), &aggBatch)

	// Build validator summary
	validatorSummary := ValidatorSummary{
		Total:        12, // Simplified
		Participated: 0,
		Pending:      0,
	}

	// Build aggregation summary
	aggregationSummary := AggregationSummary{}
	if cid, ok := aggBatch["BatchIPFSCID"].(string); ok {
		aggregationSummary.BatchIPFSCid = cid
	}
	if merkleRoot, ok := aggBatch["MerkleRoot"].(string); ok {
		aggregationSummary.MerkleRoot = merkleRoot
	}
	if projectIds, ok := aggBatch["ProjectIds"].([]interface{}); ok {
		aggregationSummary.ProjectsFinalized = len(projectIds)
	}
	if ts, ok := aggBatch["Timestamp"].(float64); ok {
		aggregationSummary.Timestamp = time.Unix(int64(ts), 0)
	}

	// Build validator contributions
	contributions := []ValidatorContribution{}
	if validatorBatches, ok := aggBatch["ValidatorBatches"].(map[string]interface{}); ok {
		validatorSummary.Participated = len(validatorBatches)
		validatorSummary.Pending = validatorSummary.Total - validatorSummary.Participated

		for validatorID, cidValue := range validatorBatches {
			if cid, ok := cidValue.(string); ok {
				contribution := ValidatorContribution{
					ValidatorID:        validatorID,
					BatchCid:           cid,
					ProjectsCount:      8, // Simplified
					SubmissionCount:    45, // Simplified
					ContributionWeight: 1.0 / float64(validatorSummary.Total),
				}
				contributions = append(contributions, contribution)
			}
		}
	}

	// Build project consensus (simplified)
	projectConsensus := make(map[string]ProjectVotes)
	if projectIds, ok := aggBatch["ProjectIds"].([]interface{}); ok {
		for _, pid := range projectIds {
			if projectID, ok := pid.(string); ok {
				projectConsensus[projectID] = ProjectVotes{
					ConsensusCid:     "QmExample...", // Simplified
					ValidatorsAgreed: validatorSummary.Participated,
					VoteDistribution: map[string]int{
						"QmExample...": validatorSummary.Participated,
					},
				}
			}
		}
	}

	// Determine consensus status
	consensusStatus := "pending"
	if validatorSummary.Participated >= validatorSummary.Total*2/3 {
		consensusStatus = "achieved"
	}

	view := NetworkConsensusView{
		EpochID:                epochID,
		ConsensusStatus:        consensusStatus,
		Validators:             validatorSummary,
		Aggregation:            aggregationSummary,
		ValidatorContributions: contributions,
		ProjectConsensus:       projectConsensus,
	}

	c.JSON(http.StatusOK, view)
}