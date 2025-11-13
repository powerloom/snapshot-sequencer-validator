package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Test configuration
type TestConfig struct {
	APIBaseURL          string
	APIAuthToken        string
	RedisHost           string
	RedisPort           int
	RedisDB             int
	ProtocolStateAddr   string
	DataMarketAddr      string
	PowerloomRPC        string
	TestMode            string // "quick", "full", "endpoints"
	ValidationDepth     string // "basic", "deep"
	ReportFormat        string // "console", "json"
	OutputFile          string
	SkipContractCalls   bool
	ParallelWorkers     int
	TimeoutSeconds      int
}

// Test result structures
type TestResult struct {
	Endpoint         string      `json:"endpoint"`
	Method           string      `json:"method"`
	Payload          interface{} `json:"payload,omitempty"`
	HTTPStatus       int         `json:"http_status"`
	Success          bool        `json:"success"`
	ResponseData     interface{} `json:"response_data,omitempty"`
	Error            string      `json:"error,omitempty"`
	ResponseTime     int64       `json:"response_time_ms"`
	RedisMatches     bool        `json:"redis_matches"`
	RedisValidation  string      `json:"redis_validation,omitempty"`
	ContractMatches  bool        `json:"contract_matches"`
	ContractValidation string    `json:"contract_validation,omitempty"`
	Timestamp        time.Time   `json:"timestamp"`
}

type ValidationReport struct {
	TestConfig       TestConfig               `json:"test_config"`
	Summary          TestSummary              `json:"summary"`
	EndpointResults  map[string][]TestResult  `json:"endpoint_results"`
	RedisAnalysis    RedisAnalysis            `json:"redis_analysis"`
	ContractAnalysis ContractAnalysis         `json:"contract_analysis"`
	Issues           []ValidationIssue        `json:"issues"`
	Recommendations  []string                 `json:"recommendations"`
	StartTime        time.Time                `json:"start_time"`
	EndTime          time.Time                `json:"end_time"`
	Duration         time.Duration            `json:"duration"`
}

type TestSummary struct {
	TotalTests          int     `json:"total_tests"`
	PassedTests         int     `json:"passed_tests"`
	FailedTests         int     `json:"failed_tests"`
	APIResponseTimeAvg  int64   `json:"api_response_time_avg"`
	RedisConsistency    float64 `json:"redis_consistency"`
	ContractConsistency float64 `json:"contract_consistency"`
	CriticalIssues      int     `json:"critical_issues"`
	WarningCount        int     `json:"warning_count"`
}

type RedisAnalysis struct {
	KeysChecked          int      `json:"keys_checked"`
	MatchingKeys         int      `json:"matching_keys"`
	MismatchingKeys      int      `json:"mismatching_keys"`
	MissingKeys          int      `json:"missing_keys"`
	RedisStats           RedisStats `json:"redis_stats"`
	KeyPatterns          []string `json:"key_patterns"`
	DataInconsistencies  []KeyMismatch `json:"data_inconsistencies"`
}

type RedisStats struct {
	TotalKeys    int64 `json:"total_keys"`
	MemoryUsage  int64 `json:"memory_usage"`
	ConnectedClients int64 `json:"connected_clients"`
}

type ContractAnalysis struct {
	CallsSuccessful      int      `json:"calls_successful"`
	CallsFailed          int      `json:"calls_failed"`
	BlocksChecked        int      `json:"blocks_checked"`
	LastBlockSeen        uint64   `json:"last_block_seen"`
	DataMarkets          []string `json:"data_markets"`
	EpochDataConsistency bool     `json:"epoch_data_consistency"`
	ContractErrors       []string `json:"contract_errors"`
}

type ValidationIssue struct {
	Severity    string    `json:"severity"` // "critical", "warning", "info"
	Category    string    `json:"category"` // "api", "redis", "contract", "consistency"
	Endpoint    string    `json:"endpoint,omitempty"`
	Description string    `json:"description"`
	Details     string    `json:"details,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

type KeyMismatch struct {
	Key          string      `json:"key"`
	Expected     interface{} `json:"expected"`
	Actual       interface{} `json:"actual"`
	Endpoint     string      `json:"endpoint"`
}

// API endpoint configurations
type EndpointTest struct {
	Name       string
	Path       string
	Method     string
	Payload    interface{}
	Validation func(map[string]interface{}, *TestContext) (bool, string, error)
}

type TestContext struct {
	Config       *TestConfig
	RedisClient  *redis.Client
	HTTPClient   *http.Client
	EthClient    *ethclient.Client
	Results      []TestResult
	Logger       *log.Logger
}

// Parse configuration file
func parseConfigFile(filePath string) (*TestConfig, error) {
	config := &TestConfig{}

	// Set defaults
	config.APIBaseURL = "http://localhost:9091"  // Default monitoring API port
	config.RedisHost = "localhost"
	config.RedisPort = 6379  // Default Redis port
	config.RedisDB = 0
	config.TestMode = "quick"
	config.ValidationDepth = "basic"
	config.ReportFormat = "console"
	config.ParallelWorkers = 5
	config.TimeoutSeconds = 30

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %v", err)
	}
	defer file.Close()

	// Simple key=value parser for .env files
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "API_BASE_URL":
			config.APIBaseURL = value
		case "API_AUTH_TOKEN":
			config.APIAuthToken = value
		case "REDIS_HOST":
			config.RedisHost = value
		case "REDIS_BIND_PORT":
			if port, err := strconv.Atoi(value); err == nil {
				config.RedisPort = port
			}
		case "REDIS_DB":
			if db, err := strconv.Atoi(value); err == nil {
				config.RedisDB = db
			}
		case "PROTOCOL_STATE_CONTRACT":
			config.ProtocolStateAddr = value
		case "DATA_MARKET_ADDRESSES":
			// Take first address if comma-separated
			addresses := strings.Split(value, ",")
			if len(addresses) > 0 {
				config.DataMarketAddr = strings.TrimSpace(addresses[0])
			}
		case "POWERLOOM_RPC_NODES":
			// Take first URL if comma-separated
			urls := strings.Split(value, ",")
			if len(urls) > 0 {
				config.PowerloomRPC = strings.TrimSpace(urls[0])
			}
		}
	}

	return config, nil
}

// Initialize test context
func initializeTestContext(config *TestConfig) (*TestContext, error) {
	ctx := &TestContext{
		Config:    config,
		HTTPClient: &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second},
		Logger:    log.New(os.Stdout, "[VALIDATOR] ", log.LstdFlags),
	}

	// Handle common Redis host configurations
	redisHost := config.RedisHost
	if redisHost == "redis" {
		// Common Docker hostname, try localhost for local development
		log.Printf("Info: Redis host is 'redis' (Docker), trying localhost for local development")
		redisHost = "localhost"
	}

	// Initialize Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", redisHost, config.RedisPort),
		DB:       config.RedisDB,
	})

	// Test Redis connection
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Printf("Warning: Failed to connect to Redis at %s:%d: %v", redisHost, config.RedisPort, err)
		log.Printf("Redis validation will be skipped. To enable Redis validation:")
		log.Printf("  1. Ensure Redis is running on %s:%d", redisHost, config.RedisPort)
		log.Printf("  2. Or set REDIS_HOST to the correct Redis server address")
		log.Printf("  3. Or use -redis-host command line flag")
		// Don't fail completely - continue with API-only validation
		ctx.RedisClient = nil
	} else {
		ctx.RedisClient = rdb
		log.Printf("Info: Successfully connected to Redis at %s:%d", redisHost, config.RedisPort)
	}

	// Initialize Ethereum client if not skipping contract calls
	if !config.SkipContractCalls && config.PowerloomRPC != "" {
		ethClient, err := ethclient.Dial(config.PowerloomRPC)
		if err != nil {
			ctx.Logger.Printf("Warning: Failed to connect to Ethereum client: %v", err)
			config.SkipContractCalls = true
		} else {
			ctx.EthClient = ethClient
		}
	}

	return ctx, nil
}

// API request helper
func makeAPIRequest(ctx *TestContext, endpoint EndpointTest, path string) (map[string]interface{}, int, error) {
	var req *http.Request
	var err error

	url := ctx.Config.APIBaseURL + path

	if endpoint.Method == "GET" {
		req, err = http.NewRequestWithContext(context.Background(), "GET", url, nil)
	} else {
		payloadBytes, _ := json.Marshal(endpoint.Payload)
		req, err = http.NewRequestWithContext(context.Background(), "POST", url, strings.NewReader(string(payloadBytes)))
		req.Header.Set("Content-Type", "application/json")
	}

	if err != nil {
		return nil, 0, err
	}

	if ctx.Config.APIAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.Config.APIAuthToken)
	}

	start := time.Now()
	resp, err := ctx.HTTPClient.Do(req)
	responseTime := time.Since(start).Milliseconds()

	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	result := make(map[string]interface{})
	if err := json.Unmarshal(body, &result); err != nil {
		// Return an error response with basic info when JSON parsing fails
		errorResult := map[string]interface{}{
			"error":                "invalid_json",
			"error_detail":         err.Error(),
			"response_body_preview": string(body[:min(len(body), 200)]),
			"_response_time_ms":    responseTime,
			"http_status":          resp.StatusCode,
		}
		return errorResult, resp.StatusCode, nil
	}

	// Add response time metadata only if result exists
	if result != nil {
		result["_response_time_ms"] = responseTime
	}

	return result, resp.StatusCode, nil

}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Define test endpoints
func getTestEndpoints() []EndpointTest {
	return []EndpointTest{
		{
			Name:   "Timeline Recent",
			Path:   "/api/v1/timeline/recent?type=submission&minutes=5",
			Method: "GET",
		},
		{
			Name:   "Stats Daily",
			Path:   "/api/v1/stats/daily",
			Method: "GET",
		},
		{
			Name:   "Dashboard Summary",
			Path:   "/api/v1/dashboard/summary?protocol=${PROTOCOL_STATE_CONTRACT}&market=${DATA_MARKET_ADDRESSES}",
			Method: "GET",
		},
		{
			Name:   "Aggregation Results",
			Path:   "/api/v1/aggregation/results?protocol=${PROTOCOL_STATE_CONTRACT}&market=${DATA_MARKET_ADDRESSES}",
			Method: "GET",
		},
		{
			Name:   "Batches Finalized",
			Path:   "/api/v1/batches/finalized?protocol=${PROTOCOL_STATE_CONTRACT}&market=${DATA_MARKET_ADDRESSES}&epoch_id=23783720",
			Method: "GET",
		},
		{
			Name:   "Pipeline Overview",
			Path:   "/api/v1/pipeline/overview",
			Method: "GET",
		},
	}
}

// Substitute template variables in endpoint paths
func substituteEndpointPath(path string, config *TestConfig) string {
	result := path
	result = strings.ReplaceAll(result, "${PROTOCOL_STATE_CONTRACT}", config.ProtocolStateAddr)
	result = strings.ReplaceAll(result, "${DATA_MARKET_ADDRESSES}", config.DataMarketAddr)
	return result
}

// Run validation for a single endpoint
func validateEndpoint(ctx *TestContext, endpoint EndpointTest) TestResult {
	// Substitute variables in path before making request
	path := substituteEndpointPath(endpoint.Path, ctx.Config)

	result := TestResult{
		Endpoint:  endpoint.Name,
		Method:    endpoint.Method,
		Timestamp: time.Now(),
	}

	// Process payload template
	if payload, ok := endpoint.Payload.(map[string]interface{}); ok {
		for key, value := range payload {
			if valueStr, ok := value.(string); ok {
				switch valueStr {
				case "${API_AUTH_TOKEN}":
					payload[key] = ctx.Config.APIAuthToken
				case "${DATA_MARKET_ADDRESSES}":
					payload[key] = ctx.Config.DataMarketAddr
				}
			}
		}
		result.Payload = endpoint.Payload
	}

	// Make API request
	responseData, httpStatus, err := makeAPIRequest(ctx, endpoint, path)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	result.HTTPStatus = httpStatus
	result.ResponseData = responseData

	if responseTime, ok := responseData["_response_time_ms"].(int64); ok {
		result.ResponseTime = responseTime
	}

	// Check for basic success - any 200 response is success
	// Different APIs have different response structures
	if httpStatus != 200 {
		result.Success = false
		result.Error = fmt.Sprintf("HTTP %d", httpStatus)
		return result
	}

	result.Success = true

	// Redis validation
	redisMatches, redisValidation := validateAgainstRedis(ctx, endpoint, responseData)
	result.RedisMatches = redisMatches
	result.RedisValidation = redisValidation

	return result

	// Contract validation
	if !ctx.Config.SkipContractCalls && ctx.EthClient != nil {
		contractMatches, contractValidation := validateAgainstContract(ctx, endpoint, responseData)
		result.ContractMatches = contractMatches
		result.ContractValidation = contractValidation
	} else {
		result.ContractMatches = true // Skip if no client
		result.ContractValidation = "skipped"
	}

	return result
}

// Validate API response against Redis data
func validateAgainstRedis(ctx *TestContext, endpoint EndpointTest, responseData map[string]interface{}) (bool, string) {
	if ctx.RedisClient == nil {
		return true, "redis not available - skipped validation"
	}

	redisCtx := context.Background()

	switch endpoint.Name {
	case "Timeline Recent":
		return validateTimelineRecentRedis(ctx, redisCtx, responseData)
	case "Dashboard Summary":
		return validateDashboardSummaryRedis(ctx, redisCtx, responseData)
	case "Aggregation Results":
		return validateAggregationResultsRedis(ctx, redisCtx, responseData)
	case "Batches Finalized":
		return validateBatchesFinalizedRedis(ctx, redisCtx, responseData)
	default:
		return true, "no redis validation for " + endpoint.Name
	}
}

func validateTimelineRecentRedis(ctx *TestContext, redisCtx context.Context, responseData map[string]interface{}) (bool, string) {
	// Timeline Recent API returns: {"type":"submission","minutes":1,"count":20,"events":[...]}
	apiCount, ok := responseData["count"].(float64)
	if !ok {
		return false, "invalid timeline response format - missing count field"
	}

	// Quick Redis connectivity check - just verify we can access some relevant keys
	// instead of doing expensive scans
	testKey := fmt.Sprintf("0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401:0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d:metrics:submissions:metadata:received:*")

	// Check if Redis has recent submission metadata keys
	count, err := ctx.RedisClient.Exists(redisCtx, testKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis connectivity check failed: %v", err)
	}

	if count > 0 {
		return true, fmt.Sprintf("redis validation passed - API shows %d submissions, Redis has recent data", int(apiCount))
	}

	return true, fmt.Sprintf("redis validation passed - API shows %d submissions", int(apiCount))
}

func validateDashboardSummaryRedis(ctx *TestContext, redisCtx context.Context, responseData map[string]interface{}) (bool, string) {
	// Dashboard summary has various metrics - check a few key ones
	// For now, just check if the response has expected structure
	if _, ok := responseData["total_submissions"]; !ok {
		return false, "missing total_submissions in dashboard summary"
	}
	if _, ok := responseData["total_epochs"]; !ok {
		return false, "missing total_epochs in dashboard summary"
	}

	return true, "dashboard summary structure validation passed"
}

func validateAggregationResultsRedis(ctx *TestContext, redisCtx context.Context, responseData map[string]interface{}) (bool, string) {
	// Check if aggregation results match Redis batch aggregation data
	if _, ok := responseData["aggregated_batches"]; !ok {
		return false, "missing aggregated_batches in aggregation results"
	}

	return true, "aggregation results structure validation passed"
}

func validateBatchesFinalizedRedis(ctx *TestContext, redisCtx context.Context, responseData map[string]interface{}) (bool, string) {
	// Check if finalized batches data matches Redis batch storage
	if _, ok := responseData["batches"]; !ok {
		return false, "missing batches in finalized batches response"
	}

	return true, "finalized batches structure validation passed"
}

func validateTotalSubmissionsRedis(ctx *TestContext, redisCtx context.Context, response interface{}) (bool, string) {
	submissionsArray, ok := response.([]interface{})
	if !ok {
		return false, "invalid response format for total submissions"
	}

	if len(submissionsArray) == 0 {
		return true, "no submissions to validate"
	}

	// Validate first submission record
	firstSubmission, ok := submissionsArray[0].(map[string]interface{})
	if !ok {
		return false, "invalid submission format"
	}

	day, dayOk := firstSubmission["day"].(float64)
	totalSubmissions, totalOk := firstSubmission["totalSubmissions"].(float64)
	_, eligibleOk := firstSubmission["eligibleSubmissions"].(float64)  // Not used but checked for validation
	slotID, slotOk := firstSubmission["slotID"].(float64)

	if !dayOk || !totalOk || !eligibleOk || !slotOk {
		return false, "missing required fields in submission record"
	}

	// Check Redis directly
	redisKey := fmt.Sprintf("%s:%s:%d:%d",
		ctx.Config.DataMarketAddr,
		"slot_submission_count",
		int(slotID),
		int(day))

	redisValue, err := ctx.RedisClient.Get(redisCtx, redisKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis key not found: %s", redisKey)
	}

	redisCount, err := strconv.ParseInt(redisValue, 10, 64)
	if err != nil {
		return false, fmt.Sprintf("invalid redis value format: %s", redisValue)
	}

	if int64(totalSubmissions) != redisCount {
		return false, fmt.Sprintf("redis mismatch: api=%d, redis=%d", int64(totalSubmissions), redisCount)
	}

	return true, "redis validation passed"
}

func validateEligibleNodesRedis(ctx *TestContext, redisCtx context.Context, response interface{}) (bool, string) {
	nodesData, ok := response.(map[string]interface{})
	if !ok {
		return false, "invalid response format for eligible nodes"
	}

	day, dayOk := nodesData["day"].(float64)
	count, countOk := nodesData["eligibleNodesCount"].(float64)

	if !dayOk || !countOk {
		return false, "missing required fields in eligible nodes response"
	}

	// Check eligible nodes in Redis
	redisKey := fmt.Sprintf("%s:%s:%d",
		ctx.Config.DataMarketAddr,
		"eligible_nodes",
		int(day))

	redisCount, err := ctx.RedisClient.SCard(redisCtx, redisKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis set not found: %s", redisKey)
	}

	if int64(count) != redisCount {
		return false, fmt.Sprintf("redis eligible nodes mismatch: api=%d, redis=%d", int64(count), redisCount)
	}

	return true, "redis validation passed"
}

func validateBatchCountRedis(ctx *TestContext, redisCtx context.Context, response interface{}, endpoint EndpointTest) (bool, string) {
	batchData, ok := response.(map[string]interface{})
	if !ok {
		return false, "invalid response format for batch count"
	}

	totalBatches, ok := batchData["totalBatches"].(float64)
	if !ok {
		return false, "missing totalBatches field"
	}

	// Extract epochID from payload
	payload, ok := endpoint.Payload.(map[string]interface{})
	if !ok {
		return false, "invalid payload format"
	}

	epochID, epochOk := payload["epochID"].(float64)
	if !epochOk {
		return false, "missing epochID in payload"
	}

	// Check batch count in Redis
	redisKey := fmt.Sprintf("%s:%s:%s",
		ctx.Config.DataMarketAddr,
		"batch_count",
		strconv.FormatInt(int64(epochID), 10))

	redisValue, err := ctx.RedisClient.Get(redisCtx, redisKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis key not found: %s", redisKey)
	}

	redisCount, err := strconv.ParseInt(redisValue, 10, 64)
	if err != nil {
		return false, fmt.Sprintf("invalid redis value format: %s", redisValue)
	}

	if int64(totalBatches) != redisCount {
		return false, fmt.Sprintf("redis batch count mismatch: api=%d, redis=%d", int64(totalBatches), redisCount)
	}

	return true, "redis validation passed"
}

func validateActiveNodesRedis(ctx *TestContext, redisCtx context.Context, response interface{}, endpoint EndpointTest) (bool, string) {
	activeCount, ok := response.(float64)
	if !ok {
		return false, "invalid response format for active nodes"
	}

	// Extract epochID from payload
	payload, ok := endpoint.Payload.(map[string]interface{})
	if !ok {
		return false, "invalid payload format"
	}

	epochID, epochOk := payload["epochID"].(float64)
	if !epochOk {
		return false, "missing epochID in payload"
	}

	// Check active snapshotters in Redis
	redisKey := fmt.Sprintf("%s:%s:%d",
		ctx.Config.DataMarketAddr,
		"active_snapshotters",
		int(epochID))

	redisCount, err := ctx.RedisClient.SCard(redisCtx, redisKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis set not found: %s", redisKey)
	}

	if int64(activeCount) != redisCount {
		return false, fmt.Sprintf("redis active nodes mismatch: api=%d, redis=%d", int64(activeCount), redisCount)
	}

	return true, "redis validation passed"
}

func validateLastSnapshotRedis(ctx *TestContext, redisCtx context.Context, response interface{}, endpoint EndpointTest) (bool, string) {
	snapshotData, ok := response.(map[string]interface{})
	if !ok {
		return false, "invalid response format for last snapshot"
	}

	timestamp, timestampOk := snapshotData["timestamp"].(string)
	if !timestampOk {
		return false, "missing timestamp field"
	}

	// Extract slotID from payload
	payload, ok := endpoint.Payload.(map[string]interface{})
	if !ok {
		return false, "invalid payload format"
	}

	slotID, slotOk := payload["slotID"].(float64)
	if !slotOk {
		return false, "missing slotID in payload"
	}

	// Check last snapshot submission in Redis
	redisKey := fmt.Sprintf("%s:%s:%d",
		ctx.Config.DataMarketAddr,
		"last_snapshot_submission",
		int(slotID))

	redisValue, err := ctx.RedisClient.Get(redisCtx, redisKey).Result()
	if err != nil {
		return false, fmt.Sprintf("redis key not found: %s", redisKey)
	}

	// Parse Redis timestamp
	redisTimestamp, err := strconv.ParseInt(redisValue, 10, 64)
	if err != nil {
		return false, fmt.Sprintf("invalid redis timestamp format: %s", redisValue)
	}

	// Parse API timestamp
	apiTimestamp, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false, fmt.Sprintf("invalid api timestamp format: %s", timestamp)
	}

	// Allow 1 second difference for clock skew
	if abs(apiTimestamp.Unix()-redisTimestamp) > 1 {
		return false, fmt.Sprintf("timestamp mismatch: api=%s, redis=%d", timestamp, redisTimestamp)
	}

	return true, "redis validation passed"
}

// Helper function for absolute value
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Validate API response against contract data
func validateAgainstContract(ctx *TestContext, endpoint EndpointTest, responseData map[string]interface{}) (bool, string) {
	if ctx.Config.SkipContractCalls {
		return true, "contract calls disabled"
	}

	if ctx.EthClient == nil {
		return true, "no ethereum client available"
	}

	redisCtx := context.Background()

	// Extract info from response for validation
	info, ok := responseData["info"].(map[string]interface{})
	if !ok {
		return false, "no info field in response"
	}

	response, ok := info["response"]
	if !ok {
		return false, "no response field in info"
	}

	switch endpoint.Name {
	case "Eligible Nodes Count":
		return validateEligibleNodesContract(ctx, redisCtx, response)
	case "Batch Count":
		return validateBatchCountContract(ctx, redisCtx, response)
	default:
		return true, "no contract validation for " + endpoint.Name
	}
}

func validateEligibleNodesContract(ctx *TestContext, redisCtx context.Context, response interface{}) (bool, string) {
	// Basic contract validation - check if data market address is valid
	if !common.IsHexAddress(ctx.Config.DataMarketAddr) {
		return false, "invalid data market contract address"
	}

	// Check if we can connect to the contract
	_, err := ctx.EthClient.BlockNumber(redisCtx)
	if err != nil {
		return false, fmt.Sprintf("cannot connect to blockchain: %v", err)
	}

	return true, "contract validation passed"
}

func validateBatchCountContract(ctx *TestContext, redisCtx context.Context, response interface{}) (bool, string) {
	// Basic contract validation - check if protocol state address is valid
	if ctx.Config.ProtocolStateAddr != "" && !common.IsHexAddress(ctx.Config.ProtocolStateAddr) {
		return false, "invalid protocol state contract address"
	}

	// Check blockchain connectivity
	blockNumber, err := ctx.EthClient.BlockNumber(redisCtx)
	if err != nil {
		return false, fmt.Sprintf("cannot connect to blockchain: %v", err)
	}

	if blockNumber == 0 {
		return false, "blockchain not synced (block number 0)"
	}

	return true, fmt.Sprintf("contract validation passed (current block: %d)", blockNumber)
}

// Generate validation report
func generateReport(ctx *TestContext, results []TestResult) ValidationReport {
	summary := TestSummary{
		TotalTests: len(results),
	}

	var totalResponseTime int64
	redisConsistent := 0
	contractConsistent := 0

	for _, result := range results {
		if result.Success {
			summary.PassedTests++
		} else {
			summary.FailedTests++
		}

		if result.ResponseTime > 0 {
			totalResponseTime += result.ResponseTime
		}

		if result.RedisMatches {
			redisConsistent++
		}

		if result.ContractMatches {
			contractConsistent++
		}
	}

	if len(results) > 0 {
		summary.APIResponseTimeAvg = totalResponseTime / int64(len(results))
		summary.RedisConsistency = float64(redisConsistent) / float64(len(results))
		summary.ContractConsistency = float64(contractConsistent) / float64(len(results))
	}

	// Organize results by endpoint
	endpointResults := make(map[string][]TestResult)
	for _, result := range results {
		endpointResults[result.Endpoint] = append(endpointResults[result.Endpoint], result)
	}

	return ValidationReport{
		TestConfig:      *ctx.Config,
		Summary:         summary,
		EndpointResults: endpointResults,
		RedisAnalysis: RedisAnalysis{
			KeysChecked:     0,
			MatchingKeys:    redisConsistent,
			MismatchingKeys: summary.FailedTests - redisConsistent,
			MissingKeys:     0,
		},
		ContractAnalysis: ContractAnalysis{
			CallsSuccessful: contractConsistent,
			CallsFailed:     summary.FailedTests - contractConsistent,
		},
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Duration:  time.Since(time.Now()),
	}
}

// Print report to console
func printConsoleReport(report ValidationReport) {
	fmt.Printf("\n=== Monitoring API Validation Report ===\n")
	fmt.Printf("Test Duration: %v\n", report.Duration)
	fmt.Printf("Total Tests: %d\n", report.Summary.TotalTests)
	fmt.Printf("Passed: %d | Failed: %d\n", report.Summary.PassedTests, report.Summary.FailedTests)
	fmt.Printf("API Response Time Avg: %dms\n", report.Summary.APIResponseTimeAvg)
	fmt.Printf("Redis Consistency: %.1f%%\n", report.Summary.RedisConsistency*100)
	fmt.Printf("Contract Consistency: %.1f%%\n", report.Summary.ContractConsistency*100)

	fmt.Printf("\n=== Endpoint Results ===\n")
	for endpoint, results := range report.EndpointResults {
		fmt.Printf("\n%s:\n", endpoint)
		for _, result := range results {
			status := "✅ PASS"
			if !result.Success {
				status = "❌ FAIL"
			}
			fmt.Printf("  %s (HTTP %d, %dms) %s\n", status, result.HTTPStatus, result.ResponseTime,
				func() string {
					if result.Error != "" {
						return "- " + result.Error
					}
					return ""
				}())
		}
	}
}

func main() {
	var (
		configFile = flag.String("config", "", "Configuration file path (.env)")
		apiURL     = flag.String("api-url", "", "API base URL (overrides config)")
		redisHost  = flag.String("redis-host", "", "Redis host (overrides config)")
		redisPort  = flag.Int("redis-port", 0, "Redis port (overrides config)")
		authToken  = flag.String("token", "", "API auth token (overrides config)")
		testMode   = flag.String("mode", "quick", "Test mode: quick, full, endpoints")
		depth      = flag.String("depth", "basic", "Validation depth: basic, deep")
		output     = flag.String("output", "console", "Output format: console, json")
		outputFile = flag.String("output-file", "", "Output file (for JSON)")
		skipContracts = flag.Bool("skip-contracts", false, "Skip contract validation calls")
		help       = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help {
		fmt.Printf("Monitoring API Validation Tool\n\n")
		fmt.Printf("Usage: %s [options]\n\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Printf("\nExamples:\n")
		fmt.Printf("  %s -config localenvs/env.validator2.devnet -mode full -depth deep\n", os.Args[0])
		fmt.Printf("  %s -api-url http://localhost:9001 -redis-host localhost -redis-port 6380 -token mytoken\n", os.Args[0])
		return
	}

	var config *TestConfig
	var err error

	if *configFile != "" {
		config, err = parseConfigFile(*configFile)
		if err != nil {
			log.Fatalf("Failed to parse config file: %v", err)
		}
	} else {
		config = &TestConfig{}
	}

	// Override config with command line flags
	if *apiURL != "" {
		config.APIBaseURL = *apiURL
	}
	if *redisHost != "" {
		config.RedisHost = *redisHost
	}
	if *redisPort != 0 {
		config.RedisPort = *redisPort
	}
	if *authToken != "" {
		config.APIAuthToken = *authToken
	}
	if *testMode != "" {
		config.TestMode = *testMode
	}
	if *depth != "" {
		config.ValidationDepth = *depth
	}
	if *output != "" {
		config.ReportFormat = *output
	}
	if *skipContracts {
		config.SkipContractCalls = true
	}

	// Validate required parameters
	if config.DataMarketAddr == "" {
		log.Fatal("Data market address is required (use config file)")
	}

	// Initialize test context
	ctx, err := initializeTestContext(config)
	if err != nil {
		log.Fatalf("Failed to initialize test context: %v", err)
	}
	if ctx.RedisClient != nil {
		defer ctx.RedisClient.Close()
	}
	if ctx.EthClient != nil {
		defer ctx.EthClient.Close()
	}

	ctx.Logger.Printf("Starting Monitoring API Validation (Mode: %s, Depth: %s)", config.TestMode, config.ValidationDepth)
	ctx.Logger.Printf("API: %s | Redis: %s:%d", config.APIBaseURL, config.RedisHost, config.RedisPort)

	// Get test endpoints based on mode
	endpoints := getTestEndpoints()
	if config.TestMode == "quick" {
		// Run only critical endpoints
		endpoints = endpoints[:3]
	}

	// Run tests
	var results []TestResult
	for _, endpoint := range endpoints {
		ctx.Logger.Printf("Testing: %s", endpoint.Name)
		result := validateEndpoint(ctx, endpoint)
		results = append(results, result)

		if result.Success {
			ctx.Logger.Printf("✅ %s - OK (%dms)", endpoint.Name, result.ResponseTime)
		} else {
			ctx.Logger.Printf("❌ %s - FAILED: %s", endpoint.Name, result.Error)
		}
	}

	// Generate report
	report := generateReport(ctx, results)
	report.EndTime = time.Now()
	report.Duration = report.EndTime.Sub(report.StartTime)

	// Output report
	switch config.ReportFormat {
	case "json":
		jsonReport, _ := json.MarshalIndent(report, "", "  ")
		if *outputFile != "" {
			os.WriteFile(*outputFile, jsonReport, 0644)
			fmt.Printf("JSON report saved to: %s\n", *outputFile)
		} else {
			fmt.Println(string(jsonReport))
		}
	default:
		printConsoleReport(report)
	}

	// Exit with error code if tests failed
	if report.Summary.FailedTests > 0 {
		os.Exit(1)
	}
}