package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// MetricType represents the type of metric being collected
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"   // Monotonically increasing value
	MetricTypeGauge     MetricType = "gauge"     // Value that can go up or down
	MetricTypeHistogram MetricType = "histogram" // Distribution of values
	MetricTypeRate      MetricType = "rate"      // Rate of change over time
	MetricTypeSummary   MetricType = "summary"   // Statistical summary with percentiles
)

// Labels represents metric labels for dimensional metrics
type Labels map[string]string

// MetricPoint represents a single metric data point
type MetricPoint struct {
	Timestamp time.Time              `json:"timestamp"`
	Value     float64                `json:"value"`
	Labels    Labels                 `json:"labels,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Metric is the interface all metric types must implement
type Metric interface {
	// Core operations
	Name() string
	Type() MetricType
	Labels() Labels
	Help() string

	// Data access
	Value() float64
	Points(since time.Time) []MetricPoint
	Reset()

	// Serialization
	Export() MetricExport
}

// Counter represents a monotonically increasing metric
type Counter struct {
	name   string
	help   string
	labels Labels
	value  atomic.Uint64

	// Track points for time-series
	mu     sync.RWMutex
	points []MetricPoint
}

// Gauge represents a metric that can go up or down
type Gauge struct {
	name   string
	help   string
	labels Labels
	value  atomic.Uint64 // Store as uint64, interpret as float64

	mu     sync.RWMutex
	points []MetricPoint
}

// Histogram tracks distribution of values
type Histogram struct {
	name    string
	help    string
	labels  Labels
	buckets []float64 // Bucket boundaries

	mu          sync.RWMutex
	counts      []uint64  // Count per bucket
	sum         float64   // Sum of all observations
	count       uint64    // Total count of observations
	points      []MetricPoint
	lastUpdate  time.Time
}

// Rate tracks the rate of change over time
type Rate struct {
	name   string
	help   string
	labels Labels
	window time.Duration

	mu         sync.RWMutex
	events     []time.Time
	lastRate   float64
	lastUpdate time.Time
	points     []MetricPoint
}

// Summary provides statistical summary with percentiles
type Summary struct {
	name   string
	help   string
	labels Labels
	maxAge time.Duration // Maximum age of samples

	mu          sync.RWMutex
	observations []observation
	sorted      bool
	points      []MetricPoint
}

type observation struct {
	value     float64
	timestamp time.Time
}

// MetricExport represents exported metric data
type MetricExport struct {
	Name      string        `json:"name"`
	Type      MetricType    `json:"type"`
	Help      string        `json:"help"`
	Labels    Labels        `json:"labels,omitempty"`
	Value     float64       `json:"value"`
	Timestamp time.Time     `json:"timestamp"`

	// Additional fields for complex types
	Buckets     []BucketExport  `json:"buckets,omitempty"`     // For histograms
	Percentiles []PercentileExport `json:"percentiles,omitempty"` // For summaries
	Rate        float64         `json:"rate,omitempty"`        // For rate metrics
	Count       uint64          `json:"count,omitempty"`       // For counters/histograms
	Sum         float64         `json:"sum,omitempty"`         // For histograms/summaries
}

// BucketExport represents histogram bucket data
type BucketExport struct {
	UpperBound float64 `json:"upper_bound"`
	Count      uint64  `json:"count"`
}

// PercentileExport represents percentile data
type PercentileExport struct {
	Percentile float64 `json:"percentile"`
	Value      float64 `json:"value"`
}

// MetricConfig holds configuration for metric creation
type MetricConfig struct {
	Name       string
	Type       MetricType
	Help       string
	Labels     Labels
	Buckets    []float64     // For histograms
	Window     time.Duration // For rate metrics
	MaxAge     time.Duration // For summaries
	Objectives map[float64]float64 // Percentiles for summaries (0.5, 0.9, 0.99, etc.)
}

// CollectorConfig holds configuration for the metrics collector
type CollectorConfig struct {
	// Redis configuration
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	RedisKeyPrefix string

	// Collection settings
	CollectionInterval time.Duration // How often to collect metrics
	BatchSize         int           // Batch size for Redis writes
	FlushInterval    time.Duration // Force flush interval
	RetentionPeriod  time.Duration // How long to keep metrics

	// Stream settings
	StreamMaxLen     int64  // Maximum stream length (0 = unlimited)
	StreamKey        string // Redis stream key for metrics

	// Export settings
	EnablePrometheus bool
	PrometheusPath   string
	PrometheusPort   int
}

// MetricUpdate represents an update to a metric
type MetricUpdate struct {
	Name      string
	Labels    Labels
	Value     float64
	Timestamp time.Time
	Operation UpdateOperation
}

// UpdateOperation represents the type of metric update
type UpdateOperation string

const (
	OpIncrement UpdateOperation = "increment"
	OpDecrement UpdateOperation = "decrement"
	OpSet       UpdateOperation = "set"
	OpObserve   UpdateOperation = "observe"
)

// AggregationType represents how metrics should be aggregated
type AggregationType string

const (
	AggSum   AggregationType = "sum"
	AggAvg   AggregationType = "avg"
	AggMin   AggregationType = "min"
	AggMax   AggregationType = "max"
	AggCount AggregationType = "count"
	AggP50   AggregationType = "p50"
	AggP90   AggregationType = "p90"
	AggP95   AggregationType = "p95"
	AggP99   AggregationType = "p99"
)

// TimeWindow represents a time window for aggregation
type TimeWindow struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
}

// AggregatedMetric represents an aggregated metric value
type AggregatedMetric struct {
	Name       string                     `json:"name"`
	Window     TimeWindow                 `json:"window"`
	Labels     Labels                     `json:"labels,omitempty"`
	Aggregates map[AggregationType]float64 `json:"aggregates"`
	Count      int                        `json:"count"`
}

// MetricQuery represents a query for metrics
type MetricQuery struct {
	Names      []string      // Metric names to query
	Labels     Labels        // Label filters
	Since      time.Time     // Start time
	Until      time.Time     // End time
	Aggregations []AggregationType // Aggregations to compute
	GroupBy    []string      // Label keys to group by
}

// MetricQueryResult represents query results
type MetricQueryResult struct {
	Metrics []AggregatedMetric `json:"metrics"`
	Query   MetricQuery        `json:"query"`
	Timestamp time.Time        `json:"timestamp"`
}