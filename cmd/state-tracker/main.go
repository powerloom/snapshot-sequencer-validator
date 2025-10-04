package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-redis/redis/v8"
	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
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
	// Setup logging
	log.SetFormatter(&logrus.JSONFormatter{})
	if os.Getenv("LOG_LEVEL") == "debug" {
		log.SetLevel(logrus.DebugLevel)
	}

	// Load configuration
	config.LoadConfig()

	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", viper.GetString("redis.host"), viper.GetString("redis.port")),
		Password: viper.GetString("redis.password"),
		DB:       viper.GetInt("redis.db"),
	})

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.WithError(err).Fatal("Failed to connect to Redis")
	}

	// Create state tracker worker
	worker := NewStateWorker(redisClient)

	// Start state change listener
	go worker.StartStateChangeListener(ctx)

	// Start aggregation workers
	go worker.StartMetricsAggregator(ctx)
	go worker.StartHourlyStatsWorker(ctx)
	go worker.StartDailyStatsWorker(ctx)
	go worker.StartPruningWorker(ctx)

	// Start metrics server (Prometheus only, no API)
	metricsPort := viper.GetInt("state_tracker.metrics_port")
	if metricsPort == 0 {
		metricsPort = 9094
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