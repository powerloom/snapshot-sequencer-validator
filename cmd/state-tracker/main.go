package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/rpcmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	log = logrus.New()

	// Prometheus metrics
	stateTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "state_tracker_transitions_total",
			Help: "Total number of state transitions",
		},
		[]string{"entity_type", "from_state", "to_state"},
	)

	activeEntities = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "state_tracker_active_entities",
			Help: "Number of active entities by type",
		},
		[]string{"entity_type"},
	)

	stateQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "state_tracker_query_duration_seconds",
			Help:    "Duration of state queries",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"query_type"},
	)
)

func init() {
	prometheus.MustRegister(stateTransitions)
	prometheus.MustRegister(activeEntities)
	prometheus.MustRegister(stateQueryDuration)
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

	// Initialize metrics client if enabled
	var metricsClient *rpcmetrics.MetricsClient
	if viper.GetBool("metrics.enabled") {
		metricsAddr := fmt.Sprintf("http://%s:%d",
			viper.GetString("metrics.host"),
			viper.GetInt("metrics.port"))
		metricsClient = rpcmetrics.NewMetricsClient(metricsAddr)
		log.WithField("metrics_endpoint", metricsAddr).Info("Metrics client initialized")
	}

	// Create state tracker
	tracker := NewStateTracker(redisClient, metricsClient)

	// Start event listener
	go tracker.StartEventListener(ctx)

	// Start HTTP API server
	apiServer := NewAPIServer(tracker)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", viper.GetInt("state_tracker.api_port")),
		Handler: apiServer.Router(),
	}

	// Start metrics server
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

	// Start HTTP server
	go func() {
		log.WithField("port", viper.GetInt("state_tracker.api_port")).Info("Starting State Tracker API server")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("API server failed")
		}
	}()

	// Start periodic cleanup of old state
	go tracker.StartCleanup(ctx)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("Shutting down State Tracker service...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.WithError(err).Error("Failed to gracefully shutdown HTTP server")
	}

	tracker.Shutdown()

	log.Info("State Tracker service stopped")
}