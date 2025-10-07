package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/go-redis/redis/v8"
	redislib "github.com/powerloom/snapshot-sequencer-validator/pkgs/redis"
	"github.com/powerloom/snapshot-sequencer-validator/config"
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


func main() {
	// Load configuration using centralized config package
	if err := config.LoadConfig(); err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}
	cfg := config.SettingsObj

	// Setup logging
	log.SetFormatter(&logrus.JSONFormatter{})
	if cfg.LogLevel == "debug" {
		log.SetLevel(logrus.DebugLevel)
	}

	// Extract protocol and market from loaded configuration
	protocol := cfg.ProtocolStateContract
	market := ""
	if len(cfg.DataMarketAddresses) > 0 {
		market = cfg.DataMarketAddresses[0]
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
	redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
	log.WithFields(logrus.Fields{
		"host": cfg.RedisHost,
		"port": cfg.RedisPort,
		"addr": redisAddr,
		"db":   cfg.RedisDB,
	}).Info("State-tracker connecting to Redis")

	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
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