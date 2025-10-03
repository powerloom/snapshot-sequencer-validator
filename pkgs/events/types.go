package events

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventType represents the type of event being emitted
type EventType string

const (
	// Epoch lifecycle events
	EventEpochStarted   EventType = "epoch_started"
	EventEpochCompleted EventType = "epoch_completed"

	// Submission events
	EventSubmissionReceived  EventType = "submission_received"
	EventSubmissionValidated EventType = "submission_validated"
	EventSubmissionRejected  EventType = "submission_rejected"

	// Window events
	EventWindowOpened EventType = "window_opened"
	EventWindowClosed EventType = "window_closed"

	// Finalization events
	EventFinalizationStarted  EventType = "finalization_started"
	EventFinalizationCompleted EventType = "finalization_completed"
	EventPartCompleted        EventType = "part_completed"

	// Aggregation events
	EventAggregationLevel1Started   EventType = "aggregation_level1_started"
	EventAggregationLevel1Completed EventType = "aggregation_level1_completed"
	EventAggregationLevel2Started   EventType = "aggregation_level2_started"
	EventAggregationLevel2Completed EventType = "aggregation_level2_completed"

	// Validator events
	EventValidatorBatchReceived EventType = "validator_batch_received"

	// Storage events
	EventIPFSStorageCompleted EventType = "ipfs_storage_completed"

	// Queue events
	EventQueueDepthChanged EventType = "queue_depth_changed"

	// Worker events
	EventWorkerStatusChanged EventType = "worker_status_changed"
)

// EventSeverity indicates the importance/severity of an event
type EventSeverity string

const (
	SeverityDebug   EventSeverity = "debug"
	SeverityInfo    EventSeverity = "info"
	SeverityWarning EventSeverity = "warning"
	SeverityError   EventSeverity = "error"
)

// Event represents a system event with metadata and payload
type Event struct {
	// Core event fields
	ID        string        `json:"id"`
	Type      EventType     `json:"type"`
	Severity  EventSeverity `json:"severity"`
	Timestamp time.Time     `json:"timestamp"`

	// Context fields
	Component   string `json:"component"`   // Component that generated the event
	Protocol    string `json:"protocol"`    // Protocol state
	DataMarket  string `json:"data_market"` // Data market
	SequencerID string `json:"sequencer_id,omitempty"`
	WorkerID    string `json:"worker_id,omitempty"`

	// Event-specific data
	Payload json.RawMessage `json:"payload"` // Event-specific payload

	// Optional fields
	EpochID      string            `json:"epoch_id,omitempty"`
	SubmissionID string            `json:"submission_id,omitempty"`
	ValidatorID  string            `json:"validator_id,omitempty"`
	Error        string            `json:"error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// EpochEventPayload contains data for epoch-related events
type EpochEventPayload struct {
	EpochID   string `json:"epoch_id"`
	StartTime int64  `json:"start_time,omitempty"`
	EndTime   int64  `json:"end_time,omitempty"`
	Duration  int64  `json:"duration_ms,omitempty"`
}

// SubmissionEventPayload contains data for submission events
type SubmissionEventPayload struct {
	SubmissionID string   `json:"submission_id"`
	EpochID      string   `json:"epoch_id"`
	ProjectID    string   `json:"project_id"`
	SnapshotCID  string   `json:"snapshot_cid"`
	SlotID       uint64   `json:"slot_id,omitempty"`
	SubmitterID  string   `json:"submitter_id,omitempty"`
	Reason       string   `json:"reason,omitempty"` // For rejection events
	ProjectIDs   []string `json:"project_ids,omitempty"` // For batch submissions
}

// WindowEventPayload contains data for window events
type WindowEventPayload struct {
	EpochID    string        `json:"epoch_id"`
	Status     string        `json:"status"`
	OpenedAt   int64         `json:"opened_at,omitempty"`
	ClosedAt   int64         `json:"closed_at,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Submissions int          `json:"submissions_count,omitempty"`
}

// FinalizationEventPayload contains data for finalization events
type FinalizationEventPayload struct {
	EpochID      string   `json:"epoch_id"`
	BatchID      string   `json:"batch_id,omitempty"`
	PartID       int      `json:"part_id,omitempty"`
	TotalParts   int      `json:"total_parts,omitempty"`
	ProjectCount int      `json:"project_count,omitempty"`
	MerkleRoot   string   `json:"merkle_root,omitempty"`
	ProjectIDs   []string `json:"project_ids,omitempty"`
	Duration     int64    `json:"duration_ms,omitempty"`
}

// AggregationEventPayload contains data for aggregation events
type AggregationEventPayload struct {
	EpochID         string   `json:"epoch_id"`
	Level           int      `json:"level"` // 1 or 2
	BatchCount      int      `json:"batch_count,omitempty"`
	ValidatorCount  int      `json:"validator_count,omitempty"`
	ProjectCount    int      `json:"project_count,omitempty"`
	ConsensusReached bool    `json:"consensus_reached,omitempty"`
	Duration        int64    `json:"duration_ms,omitempty"`
	ValidatorIDs    []string `json:"validator_ids,omitempty"`
}

// ValidatorBatchEventPayload contains data for validator batch events
type ValidatorBatchEventPayload struct {
	ValidatorID  string `json:"validator_id"`
	EpochID      string `json:"epoch_id"`
	BatchCID     string `json:"batch_cid"`
	ProjectCount int    `json:"project_count"`
	ReceivedAt   int64  `json:"received_at"`
}

// IPFSStorageEventPayload contains data for IPFS storage events
type IPFSStorageEventPayload struct {
	CID      string `json:"cid"`
	Size     int64  `json:"size,omitempty"`
	Type     string `json:"type"` // "batch", "snapshot", etc.
	EpochID  string `json:"epoch_id,omitempty"`
	Duration int64  `json:"duration_ms,omitempty"`
}

// QueueEventPayload contains data for queue depth changes
type QueueEventPayload struct {
	QueueName     string `json:"queue_name"`
	CurrentDepth  int    `json:"current_depth"`
	PreviousDepth int    `json:"previous_depth,omitempty"`
	Delta         int    `json:"delta"`
	Threshold     int    `json:"threshold,omitempty"`
	AlertLevel    string `json:"alert_level,omitempty"` // "normal", "warning", "critical"
}

// WorkerEventPayload contains data for worker status changes
type WorkerEventPayload struct {
	WorkerType      string    `json:"worker_type"` // "dequeuer", "finalizer", "aggregator"
	WorkerID        string    `json:"worker_id"`
	PreviousStatus  string    `json:"previous_status,omitempty"`
	CurrentStatus   string    `json:"current_status"`
	BatchesProcessed int      `json:"batches_processed,omitempty"`
	LastActivity    time.Time `json:"last_activity,omitempty"`
	ErrorCount      int       `json:"error_count,omitempty"`
	Uptime          int64     `json:"uptime_seconds,omitempty"`
}

// EventHandler is called when an event is emitted
type EventHandler func(event *Event)

// EventFilter can be used to filter events before processing
type EventFilter func(event *Event) bool

// Subscriber represents an event subscriber with optional filtering
type Subscriber struct {
	ID      string
	Handler EventHandler
	Filter  EventFilter
	Types   []EventType // Subscribe to specific event types only
}

// String returns a string representation of the event
func (e *Event) String() string {
	return fmt.Sprintf("[%s] %s: %s (component=%s, epoch=%s)",
		e.Timestamp.Format(time.RFC3339),
		e.Severity,
		e.Type,
		e.Component,
		e.EpochID,
	)
}

// ToJSON serializes the event to JSON
func (e *Event) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// NewEvent creates a new event with the given parameters
func NewEvent(eventType EventType, severity EventSeverity, component string, payload interface{}) (*Event, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	return &Event{
		ID:        generateEventID(),
		Type:      eventType,
		Severity:  severity,
		Timestamp: time.Now().UTC(),
		Component: component,
		Payload:   payloadBytes,
		Metadata:  make(map[string]string),
	}, nil
}

// generateEventID creates a unique event ID
func generateEventID() string {
	// Use timestamp with nanoseconds for uniqueness
	return fmt.Sprintf("evt_%d", time.Now().UnixNano())
}