package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

// APIServer provides HTTP API for state queries
type APIServer struct {
	tracker *StateTracker
}

// NewAPIServer creates a new API server
func NewAPIServer(tracker *StateTracker) *APIServer {
	return &APIServer{
		tracker: tracker,
	}
}

// Router creates the HTTP router with all endpoints
func (s *APIServer) Router() *mux.Router {
	r := mux.NewRouter()

	// API v1 routes
	api := r.PathPrefix("/api/v1").Subrouter()

	// Epoch endpoints
	api.HandleFunc("/state/epoch/{id}", s.handleGetEpochState).Methods("GET")
	api.HandleFunc("/state/epochs/active", s.handleGetActiveEpochs).Methods("GET")
	api.HandleFunc("/state/epochs/timeline", s.handleGetEpochTimeline).Methods("GET")

	// Submission endpoints
	api.HandleFunc("/state/submission/{id}", s.handleGetSubmissionState).Methods("GET")
	api.HandleFunc("/state/submissions/active", s.handleGetActiveSubmissions).Methods("GET")
	api.HandleFunc("/state/submissions/project/{projectId}", s.handleGetProjectSubmissions).Methods("GET")
	api.HandleFunc("/state/submissions/snapshotter/{snapshotter}", s.handleGetSnapshotterSubmissions).Methods("GET")

	// Validator endpoints
	api.HandleFunc("/state/validator/{id}", s.handleGetValidatorState).Methods("GET")
	api.HandleFunc("/state/validators/active", s.handleGetActiveValidators).Methods("GET")
	api.HandleFunc("/state/validators/by-stake", s.handleGetValidatorsByStake).Methods("GET")
	api.HandleFunc("/state/validator/{id}/epochs", s.handleGetValidatorEpochs).Methods("GET")
	api.HandleFunc("/state/validator/{id}/batches", s.handleGetValidatorBatches).Methods("GET")

	// Timeline endpoint
	api.HandleFunc("/state/timeline", s.handleGetTimeline).Methods("GET")

	// Health check
	api.HandleFunc("/health", s.handleHealthCheck).Methods("GET")

	// Add middleware for metrics
	r.Use(s.metricsMiddleware)

	// Add CORS middleware
	r.Use(s.corsMiddleware)

	return r
}

// metricsMiddleware tracks API request metrics
func (s *APIServer) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		duration := time.Since(start).Seconds()
		stateQueryDuration.WithLabelValues(r.URL.Path).Observe(duration)
	})
}

// corsMiddleware adds CORS headers
func (s *APIServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleGetEpochState returns state and metadata for an epoch
func (s *APIServer) handleGetEpochState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	epochID := vars["id"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("epoch_state"))
	defer timer.ObserveDuration()

	stats, err := s.tracker.epochTracker.GetEpochStats(context.Background(), epochID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	stats["epoch_id"] = epochID
	s.writeJSON(w, stats)
}

// handleGetActiveEpochs returns list of active epochs
func (s *APIServer) handleGetActiveEpochs(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("active_epochs"))
	defer timer.ObserveDuration()

	epochs, err := s.tracker.epochTracker.GetActiveEpochs(context.Background())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"count":  len(epochs),
		"epochs": epochs,
	})
}

// handleGetEpochTimeline returns epoch state transitions
func (s *APIServer) handleGetEpochTimeline(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("epoch_timeline"))
	defer timer.ObserveDuration()

	// Parse time range from query params
	start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)

	if end == 0 {
		end = time.Now().Unix()
	}
	if start == 0 {
		start = end - 3600 // Default to last hour
	}

	timeline, err := s.tracker.GetTimeline(context.Background(), "epoch", start, end)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"start":    start,
		"end":      end,
		"count":    len(timeline),
		"timeline": timeline,
	})
}

// handleGetSubmissionState returns state and metadata for a submission
func (s *APIServer) handleGetSubmissionState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	submissionID := vars["id"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("submission_state"))
	defer timer.ObserveDuration()

	stats, err := s.tracker.submissionTracker.GetSubmissionStats(context.Background(), submissionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	stats["submission_id"] = submissionID
	s.writeJSON(w, stats)
}

// handleGetActiveSubmissions returns list of active submissions
func (s *APIServer) handleGetActiveSubmissions(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("active_submissions"))
	defer timer.ObserveDuration()

	submissions, err := s.tracker.submissionTracker.GetActiveSubmissions(context.Background())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"count":       len(submissions),
		"submissions": submissions,
	})
}

// handleGetProjectSubmissions returns submissions for a project
func (s *APIServer) handleGetProjectSubmissions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	projectID := vars["projectId"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("project_submissions"))
	defer timer.ObserveDuration()

	submissions, err := s.tracker.submissionTracker.GetSubmissionsByProject(context.Background(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"project_id":  projectID,
		"count":       len(submissions),
		"submissions": submissions,
	})
}

// handleGetSnapshotterSubmissions returns submissions from a snapshotter
func (s *APIServer) handleGetSnapshotterSubmissions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	snapshotter := vars["snapshotter"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("snapshotter_submissions"))
	defer timer.ObserveDuration()

	submissions, err := s.tracker.submissionTracker.GetSubmissionsBySnapshotter(context.Background(), snapshotter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"snapshotter": snapshotter,
		"count":       len(submissions),
		"submissions": submissions,
	})
}

// handleGetValidatorState returns state and metadata for a validator
func (s *APIServer) handleGetValidatorState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	validatorID := vars["id"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("validator_state"))
	defer timer.ObserveDuration()

	stats, err := s.tracker.validatorTracker.GetValidatorStats(context.Background(), validatorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	stats["validator_id"] = validatorID
	s.writeJSON(w, stats)
}

// handleGetActiveValidators returns list of active validators
func (s *APIServer) handleGetActiveValidators(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("active_validators"))
	defer timer.ObserveDuration()

	validators, err := s.tracker.validatorTracker.GetActiveValidators(context.Background())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"count":      len(validators),
		"validators": validators,
	})
}

// handleGetValidatorsByStake returns validators sorted by stake
func (s *APIServer) handleGetValidatorsByStake(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("validators_by_stake"))
	defer timer.ObserveDuration()

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	validators, err := s.tracker.validatorTracker.GetValidatorsByStake(context.Background(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"count":      len(validators),
		"validators": validators,
	})
}

// handleGetValidatorEpochs returns epochs a validator participated in
func (s *APIServer) handleGetValidatorEpochs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	validatorID := vars["id"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("validator_epochs"))
	defer timer.ObserveDuration()

	epochs, err := s.tracker.validatorTracker.GetValidatorEpochs(context.Background(), validatorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"validator_id": validatorID,
		"count":        len(epochs),
		"epochs":       epochs,
	})
}

// handleGetValidatorBatches returns batches submitted by a validator
func (s *APIServer) handleGetValidatorBatches(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	validatorID := vars["id"]

	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("validator_batches"))
	defer timer.ObserveDuration()

	batches, err := s.tracker.validatorTracker.GetValidatorBatches(context.Background(), validatorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"validator_id": validatorID,
		"count":        len(batches),
		"batches":      batches,
	})
}

// handleGetTimeline returns state transition timeline
func (s *APIServer) handleGetTimeline(w http.ResponseWriter, r *http.Request) {
	timer := prometheus.NewTimer(stateQueryDuration.WithLabelValues("timeline"))
	defer timer.ObserveDuration()

	// Get parameters
	entityType := r.URL.Query().Get("type")
	if entityType == "" {
		entityType = "epoch" // Default to epochs
	}

	start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)

	if end == 0 {
		end = time.Now().Unix()
	}
	if start == 0 {
		start = end - 3600 // Default to last hour
	}

	timeline, err := s.tracker.GetTimeline(context.Background(), entityType, start, end)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"type":     entityType,
		"start":    start,
		"end":      end,
		"count":    len(timeline),
		"timeline": timeline,
	})
}

// handleHealthCheck returns service health status
func (s *APIServer) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Check Redis connectivity
	if err := s.tracker.redis.Ping(ctx).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.writeJSON(w, map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	// Get some basic stats
	activeEpochs, _ := s.tracker.redis.SCard(ctx, "active:epochs").Result()
	activeSubmissions, _ := s.tracker.redis.SCard(ctx, "active:submissions").Result()
	activeValidators, _ := s.tracker.redis.SCard(ctx, "active:validators").Result()

	s.writeJSON(w, map[string]interface{}{
		"status": "healthy",
		"stats": map[string]interface{}{
			"active_epochs":      activeEpochs,
			"active_submissions": activeSubmissions,
			"active_validators":  activeValidators,
		},
		"timestamp": time.Now().Unix(),
	})
}

// writeJSON writes JSON response
func (s *APIServer) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("Failed to encode JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}