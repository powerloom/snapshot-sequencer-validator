package metrics

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// TimeSeries manages time-series data for a metric
type TimeSeries struct {
	mu sync.RWMutex

	// Configuration
	name       string
	labels     Labels
	resolution time.Duration // Bucket size (e.g., 1 second, 1 minute)
	retention  time.Duration // How long to keep data

	// Data storage
	buckets map[int64]*Bucket // timestamp -> bucket
	sorted  []int64           // sorted timestamps for efficient queries
}

// Bucket represents a time bucket in the series
type Bucket struct {
	Timestamp time.Time
	Count     int
	Sum       float64
	Min       float64
	Max       float64
	Values    []float64 // Store individual values for percentiles
}

// NewTimeSeries creates a new time series
func NewTimeSeries(name string, labels Labels, resolution, retention time.Duration) *TimeSeries {
	if resolution == 0 {
		resolution = time.Second
	}
	if retention == 0 {
		retention = 24 * time.Hour
	}

	ts := &TimeSeries{
		name:       name,
		labels:     labels,
		resolution: resolution,
		retention:  retention,
		buckets:    make(map[int64]*Bucket),
		sorted:     make([]int64, 0),
	}

	// Start cleanup goroutine
	go ts.cleanup()

	return ts
}

// Add adds a value to the time series
func (ts *TimeSeries) Add(value float64, timestamp time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Round timestamp to bucket
	bucketTime := ts.roundTime(timestamp)
	bucketKey := bucketTime.Unix()

	bucket, exists := ts.buckets[bucketKey]
	if !exists {
		bucket = &Bucket{
			Timestamp: bucketTime,
			Count:     0,
			Sum:       0,
			Min:       value,
			Max:       value,
			Values:    make([]float64, 0),
		}
		ts.buckets[bucketKey] = bucket

		// Insert into sorted list
		ts.insertSorted(bucketKey)
	}

	// Update bucket
	bucket.Count++
	bucket.Sum += value
	bucket.Values = append(bucket.Values, value)

	if value < bucket.Min {
		bucket.Min = value
	}
	if value > bucket.Max {
		bucket.Max = value
	}
}

// Query retrieves data for a time range
func (ts *TimeSeries) Query(start, end time.Time) *TimeSeriesResult {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := &TimeSeriesResult{
		Name:   ts.name,
		Labels: ts.labels,
		Start:  start,
		End:    end,
		Points: make([]TimeSeriesPoint, 0),
	}

	// Find buckets in range
	startKey := ts.roundTime(start).Unix()
	endKey := ts.roundTime(end).Unix()

	// Binary search for start position
	startIdx := sort.Search(len(ts.sorted), func(i int) bool {
		return ts.sorted[i] >= startKey
	})

	// Collect points
	for i := startIdx; i < len(ts.sorted); i++ {
		key := ts.sorted[i]
		if key > endKey {
			break
		}

		bucket := ts.buckets[key]
		if bucket != nil {
			point := TimeSeriesPoint{
				Timestamp: bucket.Timestamp,
				Count:     bucket.Count,
				Sum:       bucket.Sum,
				Min:       bucket.Min,
				Max:       bucket.Max,
				Avg:       bucket.Sum / float64(bucket.Count),
			}

			// Calculate percentiles if requested
			if len(bucket.Values) > 0 {
				sorted := make([]float64, len(bucket.Values))
				copy(sorted, bucket.Values)
				sort.Float64s(sorted)

				point.P50 = percentile(sorted, 0.5)
				point.P90 = percentile(sorted, 0.9)
				point.P95 = percentile(sorted, 0.95)
				point.P99 = percentile(sorted, 0.99)
			}

			result.Points = append(result.Points, point)
		}
	}

	// Calculate overall statistics
	if len(result.Points) > 0 {
		result.Stats = ts.calculateStats(result.Points)
	}

	return result
}

// Aggregate performs aggregation over a time window
func (ts *TimeSeries) Aggregate(window time.Duration, aggregations []AggregationType) []AggregatedPoint {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if len(ts.sorted) == 0 {
		return nil
	}

	// Group buckets by window
	windowBuckets := make(map[int64][]*Bucket)
	for _, key := range ts.sorted {
		bucket := ts.buckets[key]
		if bucket != nil {
			windowKey := (bucket.Timestamp.Unix() / int64(window.Seconds())) * int64(window.Seconds())
			windowBuckets[windowKey] = append(windowBuckets[windowKey], bucket)
		}
	}

	// Create aggregated points
	result := make([]AggregatedPoint, 0, len(windowBuckets))

	for windowKey, buckets := range windowBuckets {
		point := AggregatedPoint{
			Timestamp: time.Unix(windowKey, 0),
			Window:    window,
			Values:    make(map[AggregationType]float64),
		}

		// Collect all values
		allValues := make([]float64, 0)
		for _, bucket := range buckets {
			allValues = append(allValues, bucket.Values...)
		}

		// Calculate requested aggregations
		for _, aggType := range aggregations {
			switch aggType {
			case AggSum:
				sum := 0.0
				for _, v := range allValues {
					sum += v
				}
				point.Values[AggSum] = sum

			case AggAvg:
				if len(allValues) > 0 {
					sum := 0.0
					for _, v := range allValues {
						sum += v
					}
					point.Values[AggAvg] = sum / float64(len(allValues))
				}

			case AggMin:
				if len(allValues) > 0 {
					min := allValues[0]
					for _, v := range allValues {
						if v < min {
							min = v
						}
					}
					point.Values[AggMin] = min
				}

			case AggMax:
				if len(allValues) > 0 {
					max := allValues[0]
					for _, v := range allValues {
						if v > max {
							max = v
						}
					}
					point.Values[AggMax] = max
				}

			case AggCount:
				point.Values[AggCount] = float64(len(allValues))

			case AggP50, AggP90, AggP95, AggP99:
				if len(allValues) > 0 {
					sort.Float64s(allValues)
					p := 0.0
					switch aggType {
					case AggP50:
						p = 0.5
					case AggP90:
						p = 0.9
					case AggP95:
						p = 0.95
					case AggP99:
						p = 0.99
					}
					point.Values[aggType] = percentile(allValues, p)
				}
			}
		}

		result = append(result, point)
	}

	// Sort by timestamp
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	return result
}

// Rate calculates the rate of change over time
func (ts *TimeSeries) Rate(window time.Duration) []RatePoint {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if len(ts.sorted) < 2 {
		return nil
	}

	result := make([]RatePoint, 0)
	windowSeconds := window.Seconds()

	for i := 1; i < len(ts.sorted); i++ {
		prevKey := ts.sorted[i-1]
		currKey := ts.sorted[i]

		prevBucket := ts.buckets[prevKey]
		currBucket := ts.buckets[currKey]

		if prevBucket != nil && currBucket != nil {
			timeDiff := currBucket.Timestamp.Sub(prevBucket.Timestamp).Seconds()
			if timeDiff > 0 && timeDiff <= windowSeconds {
				valueDiff := currBucket.Sum - prevBucket.Sum
				rate := valueDiff / timeDiff

				result = append(result, RatePoint{
					Timestamp: currBucket.Timestamp,
					Rate:      rate,
					Window:    time.Duration(timeDiff * float64(time.Second)),
				})
			}
		}
	}

	return result
}

// Downsample reduces resolution by aggregating buckets
func (ts *TimeSeries) Downsample(newResolution time.Duration) *TimeSeries {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if newResolution <= ts.resolution {
		return nil // Can't downsample to higher resolution
	}

	// Create new time series with lower resolution
	newTS := NewTimeSeries(
		ts.name+"_downsampled",
		ts.labels,
		newResolution,
		ts.retention,
	)

	// Group existing buckets by new resolution
	groups := make(map[int64][]*Bucket)
	for _, key := range ts.sorted {
		bucket := ts.buckets[key]
		if bucket != nil {
			newKey := (bucket.Timestamp.Unix() / int64(newResolution.Seconds())) * int64(newResolution.Seconds())
			groups[newKey] = append(groups[newKey], bucket)
		}
	}

	// Aggregate groups into new buckets
	for groupKey, buckets := range groups {
		timestamp := time.Unix(groupKey, 0)

		// Combine all values
		allValues := make([]float64, 0)
		for _, bucket := range buckets {
			allValues = append(allValues, bucket.Values...)
		}

		// Add aggregated values to new series
		for _, value := range allValues {
			newTS.Add(value, timestamp)
		}
	}

	return newTS
}

// roundTime rounds a timestamp to the nearest bucket
func (ts *TimeSeries) roundTime(t time.Time) time.Time {
	seconds := ts.resolution.Seconds()
	rounded := (t.Unix() / int64(seconds)) * int64(seconds)
	return time.Unix(rounded, 0)
}

// insertSorted inserts a key into the sorted list
func (ts *TimeSeries) insertSorted(key int64) {
	idx := sort.Search(len(ts.sorted), func(i int) bool {
		return ts.sorted[i] >= key
	})

	if idx < len(ts.sorted) && ts.sorted[idx] == key {
		return // Already exists
	}

	// Insert at position
	ts.sorted = append(ts.sorted, 0)
	copy(ts.sorted[idx+1:], ts.sorted[idx:])
	ts.sorted[idx] = key
}

// cleanup periodically removes old data
func (ts *TimeSeries) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ts.mu.Lock()

		now := time.Now()
		cutoff := now.Add(-ts.retention).Unix()

		// Find index of first bucket to keep
		keepIdx := sort.Search(len(ts.sorted), func(i int) bool {
			return ts.sorted[i] >= cutoff
		})

		// Remove old buckets
		for i := 0; i < keepIdx; i++ {
			delete(ts.buckets, ts.sorted[i])
		}

		// Update sorted list
		if keepIdx > 0 {
			ts.sorted = ts.sorted[keepIdx:]
		}

		ts.mu.Unlock()
	}
}

// calculateStats calculates statistics for a set of points
func (ts *TimeSeries) calculateStats(points []TimeSeriesPoint) TimeSeriesStats {
	stats := TimeSeriesStats{}

	if len(points) == 0 {
		return stats
	}

	for _, point := range points {
		stats.TotalCount += point.Count
		stats.TotalSum += point.Sum

		if stats.Min == 0 || point.Min < stats.Min {
			stats.Min = point.Min
		}
		if point.Max > stats.Max {
			stats.Max = point.Max
		}
	}

	stats.Avg = stats.TotalSum / float64(stats.TotalCount)

	// Calculate standard deviation
	sumSquaredDiff := 0.0
	for _, point := range points {
		diff := point.Avg - stats.Avg
		sumSquaredDiff += diff * diff * float64(point.Count)
	}
	stats.StdDev = math.Sqrt(sumSquaredDiff / float64(stats.TotalCount))

	return stats
}

// TimeSeriesResult represents query results
type TimeSeriesResult struct {
	Name   string
	Labels Labels
	Start  time.Time
	End    time.Time
	Points []TimeSeriesPoint
	Stats  TimeSeriesStats
}

// TimeSeriesPoint represents a data point in the series
type TimeSeriesPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int       `json:"count"`
	Sum       float64   `json:"sum"`
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
	Avg       float64   `json:"avg"`
	P50       float64   `json:"p50,omitempty"`
	P90       float64   `json:"p90,omitempty"`
	P95       float64   `json:"p95,omitempty"`
	P99       float64   `json:"p99,omitempty"`
}

// TimeSeriesStats represents overall statistics
type TimeSeriesStats struct {
	TotalCount int     `json:"total_count"`
	TotalSum   float64 `json:"total_sum"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	Avg        float64 `json:"avg"`
	StdDev     float64 `json:"std_dev"`
}

// AggregatedPoint represents an aggregated data point
type AggregatedPoint struct {
	Timestamp time.Time                    `json:"timestamp"`
	Window    time.Duration                `json:"window"`
	Values    map[AggregationType]float64 `json:"values"`
}

// RatePoint represents a rate calculation point
type RatePoint struct {
	Timestamp time.Time     `json:"timestamp"`
	Rate      float64       `json:"rate"`
	Window    time.Duration `json:"window"`
}

// TimeSeriesManager manages multiple time series
type TimeSeriesManager struct {
	mu     sync.RWMutex
	series map[string]*TimeSeries
	config TimeSeriesConfig
}

// TimeSeriesConfig holds configuration for time series
type TimeSeriesConfig struct {
	DefaultResolution time.Duration
	DefaultRetention  time.Duration
	MaxSeries        int
}

// NewTimeSeriesManager creates a new time series manager
func NewTimeSeriesManager(config TimeSeriesConfig) *TimeSeriesManager {
	if config.DefaultResolution == 0 {
		config.DefaultResolution = time.Second
	}
	if config.DefaultRetention == 0 {
		config.DefaultRetention = 24 * time.Hour
	}
	if config.MaxSeries == 0 {
		config.MaxSeries = 10000
	}

	return &TimeSeriesManager{
		series: make(map[string]*TimeSeries),
		config: config,
	}
}

// GetOrCreate gets or creates a time series
func (tsm *TimeSeriesManager) GetOrCreate(name string, labels Labels) *TimeSeries {
	key := fmt.Sprintf("%s:%s", name, labelsToString(labels))

	tsm.mu.RLock()
	ts, exists := tsm.series[key]
	tsm.mu.RUnlock()

	if exists {
		return ts
	}

	tsm.mu.Lock()
	defer tsm.mu.Unlock()

	// Check again after acquiring write lock
	ts, exists = tsm.series[key]
	if exists {
		return ts
	}

	// Check max series limit
	if len(tsm.series) >= tsm.config.MaxSeries {
		// Find and remove oldest series
		var oldestKey string
		var oldestTime time.Time

		for k, s := range tsm.series {
			s.mu.RLock()
			if len(s.sorted) > 0 {
				lastTime := time.Unix(s.sorted[len(s.sorted)-1], 0)
				if oldestKey == "" || lastTime.Before(oldestTime) {
					oldestKey = k
					oldestTime = lastTime
				}
			}
			s.mu.RUnlock()
		}

		if oldestKey != "" {
			delete(tsm.series, oldestKey)
		}
	}

	// Create new series
	ts = NewTimeSeries(
		name,
		labels,
		tsm.config.DefaultResolution,
		tsm.config.DefaultRetention,
	)
	tsm.series[key] = ts

	return ts
}

// List returns all time series
func (tsm *TimeSeriesManager) List() []*TimeSeries {
	tsm.mu.RLock()
	defer tsm.mu.RUnlock()

	result := make([]*TimeSeries, 0, len(tsm.series))
	for _, ts := range tsm.series {
		result = append(result, ts)
	}
	return result
}

// Remove removes a time series
func (tsm *TimeSeriesManager) Remove(name string, labels Labels) {
	key := fmt.Sprintf("%s:%s", name, labelsToString(labels))

	tsm.mu.Lock()
	defer tsm.mu.Unlock()

	delete(tsm.series, key)
}