package rpcmetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// EventMetric represents an event to be sent to the metrics service
type EventMetric struct {
	EventType string                 `json:"event_type"`
	EntityID  string                 `json:"entity_id"`
	Timestamp int64                  `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// AggregatedMetric represents aggregated metrics data
type AggregatedMetric struct {
	MetricName string                 `json:"metric_name"`
	Period     string                 `json:"period"`
	Value      float64                `json:"value"`
	Timestamp  int64                  `json:"timestamp"`
	Labels     map[string]string      `json:"labels,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// MetricsClient is a client for sending metrics to the metrics service
type MetricsClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewMetricsClient creates a new metrics client
func NewMetricsClient(baseURL string) *MetricsClient {
	return &MetricsClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// RecordEvent sends an event metric to the metrics service
func (c *MetricsClient) RecordEvent(ctx context.Context, metric *EventMetric) error {
	url := fmt.Sprintf("%s/api/v1/events", c.baseURL)

	data, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("failed to marshal event metric: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send event metric: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// RecordMetric sends an aggregated metric to the metrics service
func (c *MetricsClient) RecordMetric(ctx context.Context, metric *AggregatedMetric) error {
	url := fmt.Sprintf("%s/api/v1/metrics", c.baseURL)

	data, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("failed to marshal metric: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send metric: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// QueryMetrics queries metrics from the service
func (c *MetricsClient) QueryMetrics(ctx context.Context, metricName string, start, end int64) ([]AggregatedMetric, error) {
	url := fmt.Sprintf("%s/api/v1/metrics/query?name=%s&start=%d&end=%d", c.baseURL, metricName, start, end)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Metrics []AggregatedMetric `json:"metrics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Metrics, nil
}