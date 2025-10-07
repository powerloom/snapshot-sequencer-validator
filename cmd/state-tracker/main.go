package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-redis/redis/v8"
	redislib "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

var (
	log = logrus.New()

	// Prometheus metrics
	stateChangesProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "state_tracker_changes_processed_total",
			Help: "Total number of state changes processed",
		},
		[]string{"entity_type"},
	)

	datasetsGenerated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "state_tracker_datasets_generated_total",
			Help: "Total number of datasets generated",
		},
		[]string{"dataset_type"},
	)

	aggregationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "state_tracker_aggregation_duration_seconds",
			Help:    "Duration of data aggregation operations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"aggregation_type"},
	)
)

func init() {
	prometheus.MustRegister(stateChangesProcessed)
	prometheus.MustRegister(datasetsGenerated)
	prometheus.MustRegister(aggregationDuration)
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}


func main() {
	// Setup logging
	log.SetFormatter(&logrus.JSONFormatter{})
	if os.Getenv("LOG_LEVEL") == "debug" {
		log.SetLevel(logrus.DebugLevel)
	}

	// Get configuration from environment variables (direct loading)
	redisHost := getEnv("REDIS_HOST", "localhost")
	redisPort := getEnv("REDIS_PORT", "6379")
	protocol := getEnv("PROTOCOL_STATE_CONTRACT", "")

	// Parse DATA_MARKET_ADDRESSES (could be comma-separated or JSON array)
	marketsEnv := getEnv("DATA_MARKET_ADDRESSES", "")

	// Extract first market address (handle comma-separated or JSON array)
	var market string
	if strings.HasPrefix(marketsEnv, "[") {
		// JSON array format
		var markets []string
		if err := json.Unmarshal([]byte(marketsEnv), &markets); err == nil && len(markets) > 0 {
			market = markets[0]
		}
	} else {
		// Comma-separated format
		markets := strings.Split(marketsEnv, ",")
		if len(markets) > 0 {
			market = strings.TrimSpace(markets[0])
		}
	}

	// Validate required configuration
	if protocol == "" {
		log.Fatal("PROTOCOL_STATE_CONTRACT is required")
	}
	if market == "" {
		log.Fatal("DATA_MARKET_ADDRESSES is required")
	}

	// Log configuration for debugging
	log.WithFields(logrus.Fields{
		"protocol": protocol,
		"market":   market,
	}).Info("State-tracker configuration loaded")

	// Log Redis configuration for debugging
	redisAddr := fmt.Sprintf("%s:%s", redisHost, redisPort)
	log.WithFields(logrus.Fields{
		"host": redisHost,
		"port": redisPort,
		"addr": redisAddr,
		"db":   0,
	}).Info("State-tracker connecting to Redis")

	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: getEnv("REDIS_PASSWORD", ""),
		DB:       0,
	})

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.WithError(err).WithField("addr", redisAddr).Fatal("Failed to connect to Redis")
	}

	// Create state tracker worker
	keyBuilder := redislib.NewKeyBuilder(protocol, market)
	worker := NewStateWorker(redisClient, keyBuilder)

	// Start state change listener
	go worker.StartStateChangeListener(ctx)

	// Start aggregation workers
	go worker.StartMetricsAggregator(ctx)
	go worker.StartHourlyStatsWorker(ctx)
	go worker.StartDailyStatsWorker(ctx)
	go worker.StartPruningWorker(ctx)

	// Start metrics server (Prometheus only, no API)
	metricsPort := 9094
	if port := os.Getenv("STATE_TRACKER_METRICS_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			metricsPort = p
		}
	}

	go func() {
		metricsServer := http.NewServeMux()
		metricsServer.Handle("/metrics", promhttp.Handler())

		log.WithField("port", metricsPort).Info("Starting metrics server")
		if err := http.ListenAndServe(fmt.Sprintf(":%d", metricsPort), metricsServer); err != nil {
			log.WithError(err).Error("Metrics server failed")
		}
	}()

	log.Info("State Tracker worker started - preparing datasets for monitor-api")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("Shutting down State Tracker worker...")

	// Graceful shutdown
	worker.Shutdown()

	log.Info("State Tracker worker stopped")
}