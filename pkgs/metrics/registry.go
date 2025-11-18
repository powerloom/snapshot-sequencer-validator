package metrics

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Registry is a thread-safe metric registry
type Registry struct {
	mu      sync.RWMutex
	metrics map[string]Metric
	config  *CollectorConfig

	// Track metric updates for batch writing
	updateChan chan MetricUpdate
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

// NewRegistry creates a new metric registry
func NewRegistry(config *CollectorConfig) *Registry {
	if config == nil {
		config = &CollectorConfig{
			CollectionInterval: 10 * time.Second,
			BatchSize:         100,
			FlushInterval:    100 * time.Millisecond,
			RetentionPeriod:  24 * time.Hour,
		}
	}

	r := &Registry{
		metrics:    make(map[string]Metric),
		config:     config,
		updateChan: make(chan MetricUpdate, 10000),
		stopChan:   make(chan struct{}),
	}

	return r
}

// Register registers a new metric
func (r *Registry) Register(config MetricConfig) (Metric, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := metricKey(config.Name, config.Labels)
	if _, exists := r.metrics[key]; exists {
		return nil, fmt.Errorf("metric %s already registered", key)
	}

	var metric Metric
	switch config.Type {
	case MetricTypeCounter:
		metric = NewCounter(config.Name, config.Help, config.Labels)
	case MetricTypeGauge:
		metric = NewGauge(config.Name, config.Help, config.Labels)
	case MetricTypeHistogram:
		if len(config.Buckets) == 0 {
			config.Buckets = DefaultBuckets()
		}
		metric = NewHistogram(config.Name, config.Help, config.Labels, config.Buckets)
	case MetricTypeRate:
		if config.Window == 0 {
			config.Window = time.Minute
		}
		metric = NewRate(config.Name, config.Help, config.Labels, config.Window)
	case MetricTypeSummary:
		if config.MaxAge == 0 {
			config.MaxAge = 10 * time.Minute
		}
		metric = NewSummary(config.Name, config.Help, config.Labels, config.MaxAge)
	default:
		return nil, fmt.Errorf("unknown metric type: %s", config.Type)
	}

	r.metrics[key] = metric
	return metric, nil
}

// Get retrieves a metric by name and labels
func (r *Registry) Get(name string, labels Labels) Metric {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := metricKey(name, labels)
	return r.metrics[key]
}

// GetOrCreate gets an existing metric or creates a new one
func (r *Registry) GetOrCreate(config MetricConfig) Metric {
	metric := r.Get(config.Name, config.Labels)
	if metric != nil {
		return metric
	}

	metric, _ = r.Register(config)
	return metric
}

// List returns all registered metrics
func (r *Registry) List() []Metric {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metrics := make([]Metric, 0, len(r.metrics))
	for _, m := range r.metrics {
		metrics = append(metrics, m)
	}
	return metrics
}

// Export exports all metrics
func (r *Registry) Export() []MetricExport {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exports := make([]MetricExport, 0, len(r.metrics))
	for _, m := range r.metrics {
		exports = append(exports, m.Export())
	}
	return exports
}

// Reset resets all metrics
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, m := range r.metrics {
		m.Reset()
	}
}

// metricKey generates a unique key for a metric
func metricKey(name string, labels Labels) string {
	if len(labels) == 0 {
		return name
	}

	// Sort label keys for consistent ordering
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := name
	for _, k := range keys {
		key += fmt.Sprintf(",%s=%s", k, labels[k])
	}
	return key
}

// DefaultBuckets returns default histogram buckets
func DefaultBuckets() []float64 {
	return []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

// Counter implementation

// NewCounter creates a new counter metric
func NewCounter(name, help string, labels Labels) *Counter {
	return &Counter{
		name:   name,
		help:   help,
		labels: labels,
		points: make([]MetricPoint, 0),
	}
}

func (c *Counter) Name() string     { return c.name }
func (c *Counter) Type() MetricType { return MetricTypeCounter }
func (c *Counter) Labels() Labels   { return c.labels }
func (c *Counter) Help() string     { return c.help }

func (c *Counter) Inc() {
	c.Add(1)
}

func (c *Counter) Add(delta float64) {
	if delta < 0 {
		return // Counters can only increase
	}
	newVal := c.value.Add(uint64(delta))

	c.mu.Lock()
	c.points = append(c.points, MetricPoint{
		Timestamp: time.Now(),
		Value:     float64(newVal),
		Labels:    c.labels,
	})
	// Keep only last 1000 points
	if len(c.points) > 1000 {
		c.points = c.points[len(c.points)-1000:]
	}
	c.mu.Unlock()
}

func (c *Counter) Value() float64 {
	return float64(c.value.Load())
}

func (c *Counter) Points(since time.Time) []MetricPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []MetricPoint
	for _, p := range c.points {
		if p.Timestamp.After(since) {
			result = append(result, p)
		}
	}
	return result
}

func (c *Counter) Reset() {
	c.value.Store(0)
	c.mu.Lock()
	c.points = c.points[:0]
	c.mu.Unlock()
}

func (c *Counter) Export() MetricExport {
	return MetricExport{
		Name:      c.name,
		Type:      MetricTypeCounter,
		Help:      c.help,
		Labels:    c.labels,
		Value:     c.Value(),
		Count:     uint64(c.Value()),
		Timestamp: time.Now(),
	}
}

// Gauge implementation

// NewGauge creates a new gauge metric
func NewGauge(name, help string, labels Labels) *Gauge {
	return &Gauge{
		name:   name,
		help:   help,
		labels: labels,
		points: make([]MetricPoint, 0),
	}
}

func (g *Gauge) Name() string     { return g.name }
func (g *Gauge) Type() MetricType { return MetricTypeGauge }
func (g *Gauge) Labels() Labels   { return g.labels }
func (g *Gauge) Help() string     { return g.help }

func (g *Gauge) Set(value float64) {
	g.value.Store(math.Float64bits(value))

	g.mu.Lock()
	g.points = append(g.points, MetricPoint{
		Timestamp: time.Now(),
		Value:     value,
		Labels:    g.labels,
	})
	if len(g.points) > 1000 {
		g.points = g.points[len(g.points)-1000:]
	}
	g.mu.Unlock()
}

func (g *Gauge) Inc() {
	g.Add(1)
}

func (g *Gauge) Dec() {
	g.Add(-1)
}

func (g *Gauge) Add(delta float64) {
	for {
		oldBits := g.value.Load()
		oldVal := math.Float64frombits(oldBits)
		newVal := oldVal + delta
		newBits := math.Float64bits(newVal)
		if g.value.CompareAndSwap(oldBits, newBits) {
			g.mu.Lock()
			g.points = append(g.points, MetricPoint{
				Timestamp: time.Now(),
				Value:     newVal,
				Labels:    g.labels,
			})
			if len(g.points) > 1000 {
				g.points = g.points[len(g.points)-1000:]
			}
			g.mu.Unlock()
			break
		}
	}
}

func (g *Gauge) Value() float64 {
	return math.Float64frombits(g.value.Load())
}

func (g *Gauge) Points(since time.Time) []MetricPoint {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []MetricPoint
	for _, p := range g.points {
		if p.Timestamp.After(since) {
			result = append(result, p)
		}
	}
	return result
}

func (g *Gauge) Reset() {
	g.value.Store(0)
	g.mu.Lock()
	g.points = g.points[:0]
	g.mu.Unlock()
}

func (g *Gauge) Export() MetricExport {
	return MetricExport{
		Name:      g.name,
		Type:      MetricTypeGauge,
		Help:      g.help,
		Labels:    g.labels,
		Value:     g.Value(),
		Timestamp: time.Now(),
	}
}

// Histogram implementation

// NewHistogram creates a new histogram metric
func NewHistogram(name, help string, labels Labels, buckets []float64) *Histogram {
	sort.Float64s(buckets)
	return &Histogram{
		name:    name,
		help:    help,
		labels:  labels,
		buckets: buckets,
		counts:  make([]uint64, len(buckets)+1), // +1 for +Inf bucket
		points:  make([]MetricPoint, 0),
	}
}

func (h *Histogram) Name() string     { return h.name }
func (h *Histogram) Type() MetricType { return MetricTypeHistogram }
func (h *Histogram) Labels() Labels   { return h.labels }
func (h *Histogram) Help() string     { return h.help }

func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Find the right bucket
	bucketIdx := sort.SearchFloat64s(h.buckets, value)
	h.counts[bucketIdx]++
	h.sum += value
	h.count++
	h.lastUpdate = time.Now()

	h.points = append(h.points, MetricPoint{
		Timestamp: h.lastUpdate,
		Value:     value,
		Labels:    h.labels,
	})
	if len(h.points) > 1000 {
		h.points = h.points[len(h.points)-1000:]
	}
}

func (h *Histogram) Value() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}

func (h *Histogram) Points(since time.Time) []MetricPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []MetricPoint
	for _, p := range h.points {
		if p.Timestamp.After(since) {
			result = append(result, p)
		}
	}
	return result
}

func (h *Histogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := range h.counts {
		h.counts[i] = 0
	}
	h.sum = 0
	h.count = 0
	h.points = h.points[:0]
}

func (h *Histogram) Export() MetricExport {
	h.mu.RLock()
	defer h.mu.RUnlock()

	buckets := make([]BucketExport, 0, len(h.buckets)+1)
	cumulativeCount := uint64(0)

	for i, bound := range h.buckets {
		cumulativeCount += h.counts[i]
		buckets = append(buckets, BucketExport{
			UpperBound: bound,
			Count:      cumulativeCount,
		})
	}
	// Add +Inf bucket
	cumulativeCount += h.counts[len(h.counts)-1]
	buckets = append(buckets, BucketExport{
		UpperBound: math.Inf(1),
		Count:      cumulativeCount,
	})

	return MetricExport{
		Name:      h.name,
		Type:      MetricTypeHistogram,
		Help:      h.help,
		Labels:    h.labels,
		Value:     h.Value(),
		Count:     h.count,
		Sum:       h.sum,
		Buckets:   buckets,
		Timestamp: h.lastUpdate,
	}
}

// Rate implementation

// NewRate creates a new rate metric
func NewRate(name, help string, labels Labels, window time.Duration) *Rate {
	return &Rate{
		name:   name,
		help:   help,
		labels: labels,
		window: window,
		events: make([]time.Time, 0),
		points: make([]MetricPoint, 0),
	}
}

func (r *Rate) Name() string     { return r.name }
func (r *Rate) Type() MetricType { return MetricTypeRate }
func (r *Rate) Labels() Labels   { return r.labels }
func (r *Rate) Help() string     { return r.help }

func (r *Rate) Mark() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.events = append(r.events, now)

	// Clean old events outside window
	cutoff := now.Add(-r.window)
	newEvents := make([]time.Time, 0, len(r.events))
	for _, t := range r.events {
		if t.After(cutoff) {
			newEvents = append(newEvents, t)
		}
	}
	r.events = newEvents

	// Calculate rate
	if len(r.events) > 0 {
		r.lastRate = float64(len(r.events)) / r.window.Seconds()
		r.lastUpdate = now

		r.points = append(r.points, MetricPoint{
			Timestamp: now,
			Value:     r.lastRate,
			Labels:    r.labels,
		})
		if len(r.points) > 1000 {
			r.points = r.points[len(r.points)-1000:]
		}
	}
}

func (r *Rate) Value() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRate
}

func (r *Rate) Points(since time.Time) []MetricPoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []MetricPoint
	for _, p := range r.points {
		if p.Timestamp.After(since) {
			result = append(result, p)
		}
	}
	return result
}

func (r *Rate) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = r.events[:0]
	r.lastRate = 0
	r.points = r.points[:0]
}

func (r *Rate) Export() MetricExport {
	return MetricExport{
		Name:      r.name,
		Type:      MetricTypeRate,
		Help:      r.help,
		Labels:    r.labels,
		Value:     r.Value(),
		Rate:      r.Value(),
		Timestamp: r.lastUpdate,
	}
}

// Summary implementation

// NewSummary creates a new summary metric
func NewSummary(name, help string, labels Labels, maxAge time.Duration) *Summary {
	return &Summary{
		name:         name,
		help:         help,
		labels:       labels,
		maxAge:       maxAge,
		observations: make([]observation, 0),
		points:       make([]MetricPoint, 0),
	}
}

func (s *Summary) Name() string     { return s.name }
func (s *Summary) Type() MetricType { return MetricTypeSummary }
func (s *Summary) Labels() Labels   { return s.labels }
func (s *Summary) Help() string     { return s.help }

func (s *Summary) Observe(value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.observations = append(s.observations, observation{
		value:     value,
		timestamp: now,
	})
	s.sorted = false

	// Clean old observations
	cutoff := now.Add(-s.maxAge)
	newObs := make([]observation, 0, len(s.observations))
	for _, o := range s.observations {
		if o.timestamp.After(cutoff) {
			newObs = append(newObs, o)
		}
	}
	s.observations = newObs

	s.points = append(s.points, MetricPoint{
		Timestamp: now,
		Value:     value,
		Labels:    s.labels,
	})
	if len(s.points) > 1000 {
		s.points = s.points[len(s.points)-1000:]
	}
}

func (s *Summary) Value() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.observations) == 0 {
		return 0
	}

	sum := 0.0
	for _, o := range s.observations {
		sum += o.value
	}
	return sum / float64(len(s.observations))
}

func (s *Summary) Percentile(p float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.observations) == 0 {
		return 0
	}

	if !s.sorted {
		sort.Slice(s.observations, func(i, j int) bool {
			return s.observations[i].value < s.observations[j].value
		})
		s.sorted = true
	}

	index := int(p * float64(len(s.observations)-1))
	return s.observations[index].value
}

func (s *Summary) Points(since time.Time) []MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []MetricPoint
	for _, p := range s.points {
		if p.Timestamp.After(since) {
			result = append(result, p)
		}
	}
	return result
}

func (s *Summary) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observations = s.observations[:0]
	s.sorted = false
	s.points = s.points[:0]
}

func (s *Summary) Export() MetricExport {
	percentiles := []PercentileExport{
		{Percentile: 0.5, Value: s.Percentile(0.5)},
		{Percentile: 0.9, Value: s.Percentile(0.9)},
		{Percentile: 0.95, Value: s.Percentile(0.95)},
		{Percentile: 0.99, Value: s.Percentile(0.99)},
	}

	s.mu.RLock()
	sum := 0.0
	for _, o := range s.observations {
		sum += o.value
	}
	count := uint64(len(s.observations))
	s.mu.RUnlock()

	return MetricExport{
		Name:        s.name,
		Type:        MetricTypeSummary,
		Help:        s.help,
		Labels:      s.labels,
		Value:       s.Value(),
		Count:       count,
		Sum:         sum,
		Percentiles: percentiles,
		Timestamp:   time.Now(),
	}
}