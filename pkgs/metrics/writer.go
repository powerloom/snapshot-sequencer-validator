package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// Writer handles writing metrics to Redis Streams with batching
type Writer struct {
	redisClient *redis.Client
	config      *CollectorConfig

	// Batching
	batch      []MetricExport
	batchMu    sync.Mutex
	batchTimer *time.Timer

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Stats
	bytesWritten  uint64
	writesSuccess uint64
	writesFailed  uint64
}

// NewWriter creates a new metrics writer
func NewWriter(config *CollectorConfig) (*Writer, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	ctx, cancel = context.WithCancel(context.Background())

	w := &Writer{
		redisClient: redisClient,
		config:      config,
		batch:       make([]MetricExport, 0, config.BatchSize),
		ctx:         ctx,
		cancel:      cancel,
	}

	return w, nil
}

// Start starts the writer
func (w *Writer) Start() error {
	// Start flush worker
	w.wg.Add(1)
	go w.flushWorker()

	// Start retention worker
	w.wg.Add(1)
	go w.retentionWorker()

	log.Debug("Metrics writer started")
	return nil
}

// Stop stops the writer
func (w *Writer) Stop() error {
	log.Debug("Stopping metrics writer...")

	// Cancel context
	w.cancel()

	// Flush remaining batch
	w.flushBatch()

	// Wait for workers
	w.wg.Wait()

	// Close Redis client
	if err := w.redisClient.Close(); err != nil {
		return fmt.Errorf("failed to close Redis client: %w", err)
	}

	log.WithFields(logrus.Fields{
		"bytes_written":  w.bytesWritten,
		"writes_success": w.writesSuccess,
		"writes_failed":  w.writesFailed,
	}).Info("Metrics writer stopped")

	return nil
}

// WriteBatch writes a batch of metrics
func (w *Writer) WriteBatch(metrics []MetricExport) error {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()

	// Add to batch
	w.batch = append(w.batch, metrics...)

	// Check if batch is full
	if len(w.batch) >= w.config.BatchSize {
		return w.flushBatchLocked()
	}

	// Reset flush timer
	if w.batchTimer != nil {
		w.batchTimer.Stop()
	}
	w.batchTimer = time.AfterFunc(w.config.FlushInterval, func() {
		w.flushBatch()
	})

	return nil
}

// WriteMetric writes a single metric
func (w *Writer) WriteMetric(metric MetricExport) error {
	return w.WriteBatch([]MetricExport{metric})
}

// WriteAggregated writes an aggregated metric
func (w *Writer) WriteAggregated(metric AggregatedMetric) error {
	// Create stream key for aggregated metrics
	streamKey := fmt.Sprintf("%s:aggregated:%s",
		w.config.StreamKey,
		metric.Window.Duration.String())

	// Prepare data
	data, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("failed to marshal aggregated metric: %w", err)
	}

	// Write to stream
	args := &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: w.config.StreamMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"metric":    metric.Name,
			"window":    metric.Window.Duration.String(),
			"data":      string(data),
			"timestamp": metric.Window.End.Unix(),
		},
	}

	if err := w.redisClient.XAdd(w.ctx, args).Err(); err != nil {
		w.writesFailed++
		return fmt.Errorf("failed to write aggregated metric to stream: %w", err)
	}

	w.writesSuccess++
	w.bytesWritten += uint64(len(data))

	// Update index
	indexKey := fmt.Sprintf("%s:index:aggregated", w.config.RedisKeyPrefix)
	score := float64(metric.Window.End.Unix())
	member := fmt.Sprintf("%s:%s", metric.Name, labelsToString(metric.Labels))

	if err := w.redisClient.ZAdd(w.ctx, indexKey, redis.Z{
		Score:  score,
		Member: member,
	}).Err(); err != nil {
		log.Warnf("Failed to update aggregated metric index: %v", err)
	}

	return nil
}

// flushBatch flushes the current batch
func (w *Writer) flushBatch() {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()
	w.flushBatchLocked()
}

// flushBatchLocked flushes the batch (assumes lock is held)
func (w *Writer) flushBatchLocked() error {
	if len(w.batch) == 0 {
		return nil
	}

	// Cancel timer if set
	if w.batchTimer != nil {
		w.batchTimer.Stop()
		w.batchTimer = nil
	}

	// Group metrics by type for efficient storage
	byType := make(map[MetricType][]MetricExport)
	for _, m := range w.batch {
		byType[m.Type] = append(byType[m.Type], m)
	}

	// Write each type to its own stream
	for metricType, metrics := range byType {
		streamKey := fmt.Sprintf("%s:%s", w.config.StreamKey, metricType)

		// Build pipeline for batch write
		pipe := w.redisClient.Pipeline()

		for _, metric := range metrics {
			data, err := json.Marshal(metric)
			if err != nil {
				log.Errorf("Failed to marshal metric %s: %v", metric.Name, err)
				continue
			}

			args := &redis.XAddArgs{
				Stream: streamKey,
				MaxLen: w.config.StreamMaxLen,
				Approx: true,
				Values: map[string]interface{}{
					"name":      metric.Name,
					"type":      string(metric.Type),
					"labels":    labelsToString(metric.Labels),
					"value":     metric.Value,
					"data":      string(data),
					"timestamp": metric.Timestamp.Unix(),
				},
			}

			pipe.XAdd(w.ctx, args)
			w.bytesWritten += uint64(len(data))

			// Update metric index
			w.updateIndex(pipe, metric)
		}

		// Execute pipeline
		if _, err := pipe.Exec(w.ctx); err != nil {
			w.writesFailed += uint64(len(metrics))
			log.Errorf("Failed to write metrics batch: %v", err)
			return err
		}

		w.writesSuccess += uint64(len(metrics))
	}

	// Clear batch
	w.batch = w.batch[:0]

	return nil
}

// updateIndex updates the metric index in Redis
func (w *Writer) updateIndex(pipe redis.Pipeliner, metric MetricExport) {
	// Create index entries for efficient querying

	// By name index
	nameIndexKey := fmt.Sprintf("%s:index:name:%s", w.config.RedisKeyPrefix, metric.Name)
	score := float64(metric.Timestamp.Unix())
	member := labelsToString(metric.Labels)

	pipe.ZAdd(w.ctx, nameIndexKey, redis.Z{
		Score:  score,
		Member: member,
	})

	// By type index
	typeIndexKey := fmt.Sprintf("%s:index:type:%s", w.config.RedisKeyPrefix, metric.Type)
	pipe.ZAdd(w.ctx, typeIndexKey, redis.Z{
		Score:  score,
		Member: metric.Name,
	})

	// Label index
	for k, v := range metric.Labels {
		labelIndexKey := fmt.Sprintf("%s:index:label:%s:%s", w.config.RedisKeyPrefix, k, v)
		pipe.ZAdd(w.ctx, labelIndexKey, redis.Z{
			Score:  score,
			Member: metric.Name,
		})
	}

	// Recent metrics list (last 1000)
	recentKey := fmt.Sprintf("%s:recent", w.config.RedisKeyPrefix)
	pipe.LPush(w.ctx, recentKey, fmt.Sprintf("%s:%s", metric.Name, labelsToString(metric.Labels)))
	pipe.LTrim(w.ctx, recentKey, 0, 999)
}

// flushWorker periodically flushes metrics
func (w *Writer) flushWorker() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.flushBatch()
		}
	}
}

// retentionWorker manages metric retention
func (w *Writer) retentionWorker() {
	defer w.wg.Done()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.enforceRetention()
		}
	}
}

// enforceRetention removes old metrics beyond retention period
func (w *Writer) enforceRetention() {
	if w.config.RetentionPeriod == 0 {
		return // No retention configured
	}

	cutoff := time.Now().Add(-w.config.RetentionPeriod)
	cutoffTimestamp := strconv.FormatInt(cutoff.Unix()*1000, 10)

	// Get all stream keys
	streamPattern := fmt.Sprintf("%s:*", w.config.StreamKey)
	streams, err := w.scanKeys(streamPattern)
	if err != nil {
		log.Errorf("Failed to list streams for retention: %v", err)
		return
	}

	for _, stream := range streams {
		// Trim stream to remove old entries
		if err := w.redisClient.XTrimMinID(w.ctx, stream, cutoffTimestamp).Err(); err != nil {
			log.Warnf("Failed to trim stream %s: %v", stream, err)
		}
	}

	// Clean up old index entries
	indexPattern := fmt.Sprintf("%s:index:*", w.config.RedisKeyPrefix)
	indexes, err := w.scanKeys(indexPattern)
	if err != nil {
		log.Errorf("Failed to list indexes for retention: %v", err)
		return
	}

	cutoffScore := float64(cutoff.Unix())
	for _, index := range indexes {
		// Remove old entries from sorted sets
		if err := w.redisClient.ZRemRangeByScore(w.ctx, index, "-inf", fmt.Sprintf("%f", cutoffScore)).Err(); err != nil {
			log.Warnf("Failed to clean index %s: %v", index, err)
		}
	}
}

// Query performs a metric query
func (w *Writer) Query(query MetricQuery) (*MetricQueryResult, error) {
	result := &MetricQueryResult{
		Query:     query,
		Timestamp: time.Now(),
		Metrics:   make([]AggregatedMetric, 0),
	}

	// Build stream keys to query
	streamKeys := make([]string, 0)
	if len(query.Names) > 0 {
		for _, name := range query.Names {
			// Find metrics by name
			pattern := fmt.Sprintf("%s:index:name:%s", w.config.RedisKeyPrefix, name)
			keys, err := w.scanKeys(pattern)
			if err != nil {
				return nil, fmt.Errorf("failed to find metrics: %w", err)
			}
			streamKeys = append(streamKeys, keys...)
		}
	} else {
		// Query all metrics
		pattern := fmt.Sprintf("%s:*", w.config.StreamKey)
		keys, err := w.scanKeys(pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to list streams: %w", err)
		}
		streamKeys = keys
	}

	// Read from streams
	for _, streamKey := range streamKeys {
		entries, err := w.readStream(streamKey, query.Since, query.Until)
		if err != nil {
			log.Warnf("Failed to read stream %s: %v", streamKey, err)
			continue
		}

		// Process entries
		points := make([]MetricPoint, 0, len(entries))
		for _, entry := range entries {
			var metric MetricExport
			if dataStr, ok := entry.Values["data"].(string); ok {
				if err := json.Unmarshal([]byte(dataStr), &metric); err != nil {
					continue
				}

				// Apply label filters
				if !matchLabels(metric.Labels, query.Labels) {
					continue
				}

				points = append(points, MetricPoint{
					Timestamp: metric.Timestamp,
					Value:     metric.Value,
					Labels:    metric.Labels,
				})
			}
		}

		if len(points) == 0 {
			continue
		}

		// Compute aggregations
		agg := AggregatedMetric{
			Name: streamKey,
			Window: TimeWindow{
				Start: query.Since,
				End:   query.Until,
			},
			Count: len(points),
		}

		// Calculate aggregates
		values := make([]float64, len(points))
		for i, p := range points {
			values[i] = p.Value
		}

		agg.Aggregates = computeAggregates(values, query.Aggregations)
		result.Metrics = append(result.Metrics, agg)
	}

	return result, nil
}

// readStream reads entries from a Redis stream
func (w *Writer) readStream(key string, since, until time.Time) ([]redis.XMessage, error) {
	start := "-"
	end := "+"

	if !since.IsZero() {
		start = strconv.FormatInt(since.Unix()*1000, 10)
	}
	if !until.IsZero() {
		end = strconv.FormatInt(until.Unix()*1000, 10)
	}

	// Read stream entries
	result, err := w.redisClient.XRange(w.ctx, key, start, end).Result()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// scanKeys scans Redis keys matching pattern
func (w *Writer) scanKeys(pattern string) ([]string, error) {
	var keys []string
	var cursor uint64

	for {
		scanResult, nextCursor, err := w.redisClient.Scan(w.ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}

		keys = append(keys, scanResult...)
		cursor = nextCursor

		if cursor == 0 {
			break
		}
	}

	return keys, nil
}

// Helper functions

// labelsToString converts labels to a string representation
func labelsToString(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}

	result := ""
	for k, v := range labels {
		if result != "" {
			result += ","
		}
		result += fmt.Sprintf("%s=%s", k, v)
	}
	return result
}

// matchLabels checks if metric labels match query labels
func matchLabels(metricLabels, queryLabels Labels) bool {
	for k, v := range queryLabels {
		if metricLabels[k] != v {
			return false
		}
	}
	return true
}

// computeAggregates computes requested aggregations
func computeAggregates(values []float64, types []AggregationType) map[AggregationType]float64 {
	if len(values) == 0 {
		return nil
	}

	result := make(map[AggregationType]float64)

	for _, aggType := range types {
		switch aggType {
		case AggSum:
			sum := 0.0
			for _, v := range values {
				sum += v
			}
			result[AggSum] = sum

		case AggAvg:
			sum := 0.0
			for _, v := range values {
				sum += v
			}
			result[AggAvg] = sum / float64(len(values))

		case AggMin:
			min := values[0]
			for _, v := range values {
				if v < min {
					min = v
				}
			}
			result[AggMin] = min

		case AggMax:
			max := values[0]
			for _, v := range values {
				if v > max {
					max = v
				}
			}
			result[AggMax] = max

		case AggCount:
			result[AggCount] = float64(len(values))

			// Percentiles require sorted values
		case AggP50, AggP90, AggP95, AggP99:
			// Implementation would sort and calculate percentiles
			// Omitted for brevity
		}
	}

	return result
}