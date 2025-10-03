package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/pkgs/events"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Collector manages metric collection, aggregation, and event integration
type Collector struct {
	registry *Registry
	writer   *Writer
	config   *CollectorConfig

	// Event integration
	eventChan chan *events.Event

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Metrics for the collector itself
	metricsCollected *Counter
	metricsDropped   *Counter
	collectionErrors *Counter
}

// NewCollector creates a new metrics collector
func NewCollector(config *CollectorConfig) (*Collector, error) {
	if config == nil {
		config = DefaultCollectorConfig()
	}

	registry := NewRegistry(config)
	writer, err := NewWriter(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create writer: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Collector{
		registry:  registry,
		writer:    writer,
		config:    config,
		eventChan: make(chan *events.Event, 1000),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Register internal metrics
	metric1, _ := registry.Register(MetricConfig{
		Name: "metrics.collected.total",
		Type: MetricTypeCounter,
		Help: "Total metrics collected",
	})
	c.metricsCollected = metric1.(*Counter)

	metric2, _ := registry.Register(MetricConfig{
		Name: "metrics.dropped.total",
		Type: MetricTypeCounter,
		Help: "Total metrics dropped",
	})
	c.metricsDropped = metric2.(*Counter)

	metric3, _ := registry.Register(MetricConfig{
		Name: "metrics.errors.total",
		Type: MetricTypeCounter,
		Help: "Total collection errors",
	})
	c.collectionErrors = metric3.(*Counter)

	return c, nil
}

// Start starts the collector
func (c *Collector) Start() error {
	// Start the writer
	if err := c.writer.Start(); err != nil {
		return fmt.Errorf("failed to start writer: %w", err)
	}

	// Start collection goroutine
	c.wg.Add(1)
	go c.collectLoop()

	// Start event processing
	c.wg.Add(1)
	go c.processEvents()

	// Start aggregation worker
	c.wg.Add(1)
	go c.aggregationLoop()

	log.Info("Metrics collector started")
	return nil
}

// Stop stops the collector
func (c *Collector) Stop() error {
	log.Info("Stopping metrics collector...")

	c.cancel()
	c.wg.Wait()

	if err := c.writer.Stop(); err != nil {
		return fmt.Errorf("failed to stop writer: %w", err)
	}

	log.Info("Metrics collector stopped")
	return nil
}

// RegisterMetric registers a new metric
func (c *Collector) RegisterMetric(config MetricConfig) (Metric, error) {
	return c.registry.Register(config)
}

// GetMetric retrieves a metric
func (c *Collector) GetMetric(name string, labels Labels) Metric {
	return c.registry.Get(name, labels)
}

// GetOrCreateMetric gets or creates a metric
func (c *Collector) GetOrCreateMetric(config MetricConfig) Metric {
	return c.registry.GetOrCreate(config)
}

// ProcessEvent processes an event and updates relevant metrics
func (c *Collector) ProcessEvent(event *events.Event) {
	select {
	case c.eventChan <- event:
	default:
		c.metricsDropped.Inc()
		log.Warn("Event channel full, dropping event")
	}
}

// collectLoop periodically collects and writes metrics
func (c *Collector) collectLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.CollectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// collect performs a collection cycle
func (c *Collector) collect() {
	metrics := c.registry.Export()

	batch := make([]MetricExport, 0, c.config.BatchSize)
	for _, m := range metrics {
		batch = append(batch, m)

		if len(batch) >= c.config.BatchSize {
			if err := c.writer.WriteBatch(batch); err != nil {
				log.Errorf("Failed to write metric batch: %v", err)
				c.collectionErrors.Inc()
			} else {
				c.metricsCollected.Add(float64(len(batch)))
			}
			batch = batch[:0]
		}
	}

	// Write remaining metrics
	if len(batch) > 0 {
		if err := c.writer.WriteBatch(batch); err != nil {
			log.Errorf("Failed to write final metric batch: %v", err)
			c.collectionErrors.Inc()
		} else {
			c.metricsCollected.Add(float64(len(batch)))
		}
	}
}

// processEvents processes events and updates metrics
func (c *Collector) processEvents() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case event := <-c.eventChan:
			c.updateMetricsFromEvent(event)
		}
	}
}

// updateMetricsFromEvent updates metrics based on an event
func (c *Collector) updateMetricsFromEvent(event *events.Event) {
	// Common labels for the event
	labels := Labels{
		"component":  event.Component,
		"protocol":   event.Protocol,
		"datamarket": event.DataMarket,
	}

	switch event.Type {
	case events.EventSubmissionReceived:
		metric := c.GetOrCreateMetric(MetricConfig{
			Name:   "submissions.received.count",
			Type:   MetricTypeCounter,
			Labels: labels,
		})
		if counter, ok := metric.(*Counter); ok {
			counter.Inc()
		}

		// Update rate metric
		rateMetric := c.GetOrCreateMetric(MetricConfig{
			Name:   "submissions.received.rate",
			Type:   MetricTypeRate,
			Labels: labels,
			Window: time.Minute,
		})
		if rate, ok := rateMetric.(*Rate); ok {
			rate.Mark()
		}

	case events.EventSubmissionValidated:
		metric := c.GetOrCreateMetric(MetricConfig{
			Name:   "submissions.validated.count",
			Type:   MetricTypeCounter,
			Labels: labels,
		})
		if counter, ok := metric.(*Counter); ok {
			counter.Inc()
		}

	case events.EventSubmissionRejected:
		metric := c.GetOrCreateMetric(MetricConfig{
			Name:   "submissions.rejected.count",
			Type:   MetricTypeCounter,
			Labels: labels,
		})
		if counter, ok := metric.(*Counter); ok {
			counter.Inc()
		}

	case events.EventFinalizationCompleted:
		// Extract duration from payload if available
		var payload events.FinalizationEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.Duration > 0 {
			metric := c.GetOrCreateMetric(MetricConfig{
				Name:   "finalization.duration",
				Type:   MetricTypeHistogram,
				Labels: labels,
			})
			if hist, ok := metric.(*Histogram); ok {
				hist.Observe(float64(payload.Duration))
			}
		}

	case events.EventAggregationLevel1Completed, events.EventAggregationLevel2Completed:
		var payload events.AggregationEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			levelLabel := labels
			levelLabel["level"] = fmt.Sprintf("%d", payload.Level)

			// Track duration
			if payload.Duration > 0 {
				metric := c.GetOrCreateMetric(MetricConfig{
					Name:   "aggregation.duration",
					Type:   MetricTypeHistogram,
					Labels: levelLabel,
				})
				if hist, ok := metric.(*Histogram); ok {
					hist.Observe(float64(payload.Duration))
				}
			}

			// Track validator participation for Level 2
			if payload.Level == 2 && payload.ValidatorCount > 0 {
				metric := c.GetOrCreateMetric(MetricConfig{
					Name:   "validator.participation.count",
					Type:   MetricTypeGauge,
					Labels: labels,
				})
				if gauge, ok := metric.(*Gauge); ok {
					gauge.Set(float64(payload.ValidatorCount))
				}
			}
		}

	case events.EventQueueDepthChanged:
		var payload events.QueueEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			queueLabels := labels
			queueLabels["queue"] = payload.QueueName

			metric := c.GetOrCreateMetric(MetricConfig{
				Name:   "queue.depth",
				Type:   MetricTypeGauge,
				Labels: queueLabels,
			})
			if gauge, ok := metric.(*Gauge); ok {
				gauge.Set(float64(payload.CurrentDepth))
			}
		}

	case events.EventWorkerStatusChanged:
		var payload events.WorkerEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			workerLabels := labels
			workerLabels["worker_type"] = payload.WorkerType
			workerLabels["worker_id"] = payload.WorkerID

			// Track utilization based on status
			utilization := 0.0
			if payload.CurrentStatus == "busy" {
				utilization = 1.0
			}

			metric := c.GetOrCreateMetric(MetricConfig{
				Name:   "worker.utilization",
				Type:   MetricTypeGauge,
				Labels: workerLabels,
			})
			if gauge, ok := metric.(*Gauge); ok {
				gauge.Set(utilization)
			}
		}

	case events.EventIPFSStorageCompleted:
		var payload events.IPFSStorageEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.Duration > 0 {
			metric := c.GetOrCreateMetric(MetricConfig{
				Name:   "ipfs.storage.duration",
				Type:   MetricTypeHistogram,
				Labels: labels,
			})
			if hist, ok := metric.(*Histogram); ok {
				hist.Observe(float64(payload.Duration))
			}
		}

	case events.EventEpochCompleted:
		var payload events.EpochEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.Duration > 0 {
			metric := c.GetOrCreateMetric(MetricConfig{
				Name:   "epoch.processing.time",
				Type:   MetricTypeHistogram,
				Labels: labels,
			})
			if hist, ok := metric.(*Histogram); ok {
				hist.Observe(float64(payload.Duration))
			}
		}
	}
}

// aggregationLoop performs periodic aggregation of metrics
func (c *Collector) aggregationLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.aggregateMetrics()
		}
	}
}

// aggregateMetrics performs metric aggregation
func (c *Collector) aggregateMetrics() {
	// Aggregate over different time windows
	windows := []time.Duration{
		time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		time.Hour,
	}

	now := time.Now()
	for _, window := range windows {
		since := now.Add(-window)

		for _, metric := range c.registry.List() {
			points := metric.Points(since)
			if len(points) == 0 {
				continue
			}

			agg := c.computeAggregations(points)

			// Store aggregated metrics
			aggMetric := AggregatedMetric{
				Name: metric.Name(),
				Window: TimeWindow{
					Start:    since,
					End:      now,
					Duration: window,
				},
				Labels:     metric.Labels(),
				Aggregates: agg,
				Count:      len(points),
			}

			// Write aggregated metric
			if err := c.writer.WriteAggregated(aggMetric); err != nil {
				log.Errorf("Failed to write aggregated metric: %v", err)
				c.collectionErrors.Inc()
			}
		}
	}
}

// computeAggregations computes various aggregations for metric points
func (c *Collector) computeAggregations(points []MetricPoint) map[AggregationType]float64 {
	if len(points) == 0 {
		return nil
	}

	values := make([]float64, len(points))
	sum := 0.0
	for i, p := range points {
		values[i] = p.Value
		sum += p.Value
	}

	sort.Float64s(values)

	agg := make(map[AggregationType]float64)
	agg[AggSum] = sum
	agg[AggAvg] = sum / float64(len(values))
	agg[AggMin] = values[0]
	agg[AggMax] = values[len(values)-1]
	agg[AggCount] = float64(len(values))

	// Percentiles
	agg[AggP50] = percentile(values, 0.5)
	agg[AggP90] = percentile(values, 0.9)
	agg[AggP95] = percentile(values, 0.95)
	agg[AggP99] = percentile(values, 0.99)

	return agg
}

// Query performs a metric query
func (c *Collector) Query(query MetricQuery) (*MetricQueryResult, error) {
	return c.writer.Query(query)
}

// Export exports all current metrics
func (c *Collector) Export() []MetricExport {
	return c.registry.Export()
}

// GetRegistry returns the metric registry
func (c *Collector) GetRegistry() *Registry {
	return c.registry
}

// DefaultCollectorConfig returns default collector configuration
func DefaultCollectorConfig() *CollectorConfig {
	return &CollectorConfig{
		CollectionInterval: 10 * time.Second,
		BatchSize:         100,
		FlushInterval:    100 * time.Millisecond,
		RetentionPeriod:  24 * time.Hour,
		StreamMaxLen:     100000,
		StreamKey:        "metrics:stream",
	}
}

// percentile calculates the percentile value from sorted slice
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}

	index := p * float64(len(sorted)-1)
	lower := int(index)
	upper := lower + 1

	if upper >= len(sorted) {
		return sorted[lower]
	}

	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// Helper functions for common metric operations

// IncrementCounter increments a counter metric
func (c *Collector) IncrementCounter(name string, labels Labels) {
	metric := c.GetOrCreateMetric(MetricConfig{
		Name:   name,
		Type:   MetricTypeCounter,
		Labels: labels,
	})
	if counter, ok := metric.(*Counter); ok {
		counter.Inc()
	}
}

// SetGauge sets a gauge metric value
func (c *Collector) SetGauge(name string, value float64, labels Labels) {
	metric := c.GetOrCreateMetric(MetricConfig{
		Name:   name,
		Type:   MetricTypeGauge,
		Labels: labels,
	})
	if gauge, ok := metric.(*Gauge); ok {
		gauge.Set(value)
	}
}

// ObserveHistogram records a histogram observation
func (c *Collector) ObserveHistogram(name string, value float64, labels Labels) {
	metric := c.GetOrCreateMetric(MetricConfig{
		Name:   name,
		Type:   MetricTypeHistogram,
		Labels: labels,
	})
	if hist, ok := metric.(*Histogram); ok {
		hist.Observe(value)
	}
}

// MarkRate marks an event for rate calculation
func (c *Collector) MarkRate(name string, labels Labels) {
	metric := c.GetOrCreateMetric(MetricConfig{
		Name:   name,
		Type:   MetricTypeRate,
		Labels: labels,
		Window: time.Minute,
	})
	if rate, ok := metric.(*Rate); ok {
		rate.Mark()
	}
}