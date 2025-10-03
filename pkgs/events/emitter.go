package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// DefaultBufferSize is the default size of the event buffer
	DefaultBufferSize = 1000

	// DefaultMaxWorkers is the default number of concurrent event processors
	DefaultMaxWorkers = 10

	// DefaultEventTimeout is the maximum time to wait for event processing
	DefaultEventTimeout = 5 * time.Second
)

// EmitterConfig contains configuration for the event emitter
type EmitterConfig struct {
	BufferSize      int           // Size of the event buffer channel
	MaxWorkers      int           // Maximum concurrent event processors
	EventTimeout    time.Duration // Timeout for event processing
	EnableMetrics   bool          // Enable metrics collection
	DropOnOverflow  bool          // Drop events if buffer is full
	Protocol        string        // Protocol state
	DataMarket      string        // Data market
	SequencerID     string        // Sequencer/Validator ID
}

// DefaultConfig returns a default configuration
func DefaultConfig() *EmitterConfig {
	return &EmitterConfig{
		BufferSize:     DefaultBufferSize,
		MaxWorkers:     DefaultMaxWorkers,
		EventTimeout:   DefaultEventTimeout,
		EnableMetrics:  true,
		DropOnOverflow: true,
	}
}

// Emitter is a thread-safe event emitter with async processing
type Emitter struct {
	// Configuration
	config *EmitterConfig

	// Event channel for async processing
	eventChan chan *Event

	// Subscribers management
	subscribers map[string]*Subscriber
	subMutex    sync.RWMutex

	// Worker pool management
	workerPool chan struct{}
	wg         sync.WaitGroup

	// Metrics
	eventsEmitted   uint64
	eventsDropped   uint64
	eventsProcessed uint64
	processingErrors uint64

	// Lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	running atomic.Bool
}

// NewEmitter creates a new event emitter with the given configuration
func NewEmitter(config *EmitterConfig) *Emitter {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	emitter := &Emitter{
		config:      config,
		eventChan:   make(chan *Event, config.BufferSize),
		subscribers: make(map[string]*Subscriber),
		workerPool:  make(chan struct{}, config.MaxWorkers),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize worker pool
	for i := 0; i < config.MaxWorkers; i++ {
		emitter.workerPool <- struct{}{}
	}

	return emitter
}

// Start begins processing events
func (e *Emitter) Start() error {
	if !e.running.CompareAndSwap(false, true) {
		return fmt.Errorf("emitter already running")
	}

	log.Info("Starting event emitter")

	// Start event processor
	e.wg.Add(1)
	go e.processEvents()

	// Start metrics reporter if enabled
	if e.config.EnableMetrics {
		e.wg.Add(1)
		go e.reportMetrics()
	}

	return nil
}

// Stop gracefully shuts down the emitter
func (e *Emitter) Stop() error {
	if !e.running.CompareAndSwap(true, false) {
		return fmt.Errorf("emitter not running")
	}

	log.Info("Stopping event emitter")

	// Cancel context to signal shutdown
	e.cancel()

	// Wait for processing to complete with timeout
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("Event emitter stopped gracefully")
	case <-time.After(10 * time.Second):
		log.Warn("Event emitter shutdown timeout, some events may be lost")
	}

	// Close event channel
	close(e.eventChan)

	return nil
}

// Emit sends an event asynchronously
func (e *Emitter) Emit(event *Event) error {
	if !e.running.Load() {
		return fmt.Errorf("emitter not running")
	}

	// Enrich event with emitter configuration
	if event.Protocol == "" {
		event.Protocol = e.config.Protocol
	}
	if event.DataMarket == "" {
		event.DataMarket = e.config.DataMarket
	}
	if event.SequencerID == "" {
		event.SequencerID = e.config.SequencerID
	}

	// Update metrics
	atomic.AddUint64(&e.eventsEmitted, 1)

	// Try to send event to channel
	select {
	case e.eventChan <- event:
		return nil
	default:
		// Buffer is full
		if e.config.DropOnOverflow {
			atomic.AddUint64(&e.eventsDropped, 1)
			log.WithFields(log.Fields{
				"event_type": event.Type,
				"event_id":   event.ID,
				"component":  event.Component,
			}).Warn("Event dropped due to buffer overflow")
			return fmt.Errorf("event buffer full, event dropped")
		}

		// Block until space is available or context is cancelled
		select {
		case e.eventChan <- event:
			return nil
		case <-e.ctx.Done():
			return fmt.Errorf("emitter shutting down")
		}
	}
}

// EmitWithTimeout sends an event with a specific timeout
func (e *Emitter) EmitWithTimeout(event *Event, timeout time.Duration) error {
	if !e.running.Load() {
		return fmt.Errorf("emitter not running")
	}

	// Enrich event
	if event.Protocol == "" {
		event.Protocol = e.config.Protocol
	}
	if event.DataMarket == "" {
		event.DataMarket = e.config.DataMarket
	}
	if event.SequencerID == "" {
		event.SequencerID = e.config.SequencerID
	}

	atomic.AddUint64(&e.eventsEmitted, 1)

	// Try to send with timeout
	select {
	case e.eventChan <- event:
		return nil
	case <-time.After(timeout):
		atomic.AddUint64(&e.eventsDropped, 1)
		return fmt.Errorf("timeout sending event")
	case <-e.ctx.Done():
		return fmt.Errorf("emitter shutting down")
	}
}

// Subscribe adds a new subscriber for events
func (e *Emitter) Subscribe(subscriber *Subscriber) error {
	if subscriber == nil || subscriber.ID == "" {
		return fmt.Errorf("invalid subscriber")
	}

	e.subMutex.Lock()
	defer e.subMutex.Unlock()

	if _, exists := e.subscribers[subscriber.ID]; exists {
		return fmt.Errorf("subscriber %s already exists", subscriber.ID)
	}

	e.subscribers[subscriber.ID] = subscriber
	log.WithField("subscriber_id", subscriber.ID).Debug("Subscriber added")

	return nil
}

// Unsubscribe removes a subscriber
func (e *Emitter) Unsubscribe(subscriberID string) error {
	e.subMutex.Lock()
	defer e.subMutex.Unlock()

	if _, exists := e.subscribers[subscriberID]; !exists {
		return fmt.Errorf("subscriber %s not found", subscriberID)
	}

	delete(e.subscribers, subscriberID)
	log.WithField("subscriber_id", subscriberID).Debug("Subscriber removed")

	return nil
}

// processEvents is the main event processing loop
func (e *Emitter) processEvents() {
	defer e.wg.Done()

	for {
		select {
		case event, ok := <-e.eventChan:
			if !ok {
				// Channel closed
				return
			}

			// Process event with a worker from the pool
			select {
			case token := <-e.workerPool:
				e.wg.Add(1)
				go func() {
					defer func() {
						e.workerPool <- token
						e.wg.Done()
					}()
					e.handleEvent(event)
				}()
			case <-e.ctx.Done():
				return
			}

		case <-e.ctx.Done():
			// Drain remaining events with timeout
			e.drainEvents()
			return
		}
	}
}

// handleEvent processes a single event
func (e *Emitter) handleEvent(event *Event) {
	atomic.AddUint64(&e.eventsProcessed, 1)

	// Get current subscribers snapshot
	e.subMutex.RLock()
	subscribers := make([]*Subscriber, 0, len(e.subscribers))
	for _, sub := range e.subscribers {
		// Check if subscriber is interested in this event type
		if len(sub.Types) > 0 {
			interested := false
			for _, eventType := range sub.Types {
				if eventType == event.Type {
					interested = true
					break
				}
			}
			if !interested {
				continue
			}
		}

		// Apply filter if present
		if sub.Filter != nil && !sub.Filter(event) {
			continue
		}

		subscribers = append(subscribers, sub)
	}
	e.subMutex.RUnlock()

	// Notify all interested subscribers
	for _, sub := range subscribers {
		// Run handler with timeout
		done := make(chan struct{})
		go func(s *Subscriber) {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					atomic.AddUint64(&e.processingErrors, 1)
					log.WithFields(log.Fields{
						"subscriber_id": s.ID,
						"event_type":    event.Type,
						"error":         r,
					}).Error("Panic in event handler")
				}
			}()

			s.Handler(event)
		}(sub)

		select {
		case <-done:
			// Handler completed
		case <-time.After(e.config.EventTimeout):
			atomic.AddUint64(&e.processingErrors, 1)
			log.WithFields(log.Fields{
				"subscriber_id": sub.ID,
				"event_type":    event.Type,
				"event_id":      event.ID,
			}).Warn("Event handler timeout")
		}
	}
}

// drainEvents processes remaining events during shutdown
func (e *Emitter) drainEvents() {
	timeout := time.After(5 * time.Second)

	for {
		select {
		case event := <-e.eventChan:
			if event != nil {
				e.handleEvent(event)
			}
		case <-timeout:
			remaining := len(e.eventChan)
			if remaining > 0 {
				log.Warnf("Shutdown timeout, dropping %d events", remaining)
			}
			return
		default:
			// No more events
			return
		}
	}
}

// reportMetrics periodically logs metrics
func (e *Emitter) reportMetrics() {
	defer e.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			emitted := atomic.LoadUint64(&e.eventsEmitted)
			dropped := atomic.LoadUint64(&e.eventsDropped)
			processed := atomic.LoadUint64(&e.eventsProcessed)
			errors := atomic.LoadUint64(&e.processingErrors)

			log.WithFields(log.Fields{
				"events_emitted":    emitted,
				"events_dropped":    dropped,
				"events_processed":  processed,
				"processing_errors": errors,
				"buffer_usage":      len(e.eventChan),
				"buffer_capacity":   cap(e.eventChan),
				"subscribers":       len(e.subscribers),
			}).Info("Event emitter metrics")

		case <-e.ctx.Done():
			return
		}
	}
}

// GetMetrics returns current emitter metrics
func (e *Emitter) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"events_emitted":    atomic.LoadUint64(&e.eventsEmitted),
		"events_dropped":    atomic.LoadUint64(&e.eventsDropped),
		"events_processed":  atomic.LoadUint64(&e.eventsProcessed),
		"processing_errors": atomic.LoadUint64(&e.processingErrors),
		"buffer_usage":      len(e.eventChan),
		"buffer_capacity":   cap(e.eventChan),
		"subscribers":       len(e.subscribers),
		"running":           e.running.Load(),
	}
}

// Helper methods for common event types

// EmitEpochStarted emits an epoch started event
func (e *Emitter) EmitEpochStarted(component string, epochID string) error {
	payload := &EpochEventPayload{
		EpochID:   epochID,
		StartTime: time.Now().Unix(),
	}

	event, err := NewEvent(EventEpochStarted, SeverityInfo, component, payload)
	if err != nil {
		return err
	}
	event.EpochID = epochID

	return e.Emit(event)
}

// EmitSubmissionReceived emits a submission received event
func (e *Emitter) EmitSubmissionReceived(component string, submission *SubmissionEventPayload) error {
	event, err := NewEvent(EventSubmissionReceived, SeverityInfo, component, submission)
	if err != nil {
		return err
	}
	event.EpochID = submission.EpochID
	event.SubmissionID = submission.SubmissionID

	return e.Emit(event)
}

// EmitQueueDepthChanged emits a queue depth change event
func (e *Emitter) EmitQueueDepthChanged(component string, queueName string, currentDepth, previousDepth int) error {
	payload := &QueueEventPayload{
		QueueName:     queueName,
		CurrentDepth:  currentDepth,
		PreviousDepth: previousDepth,
		Delta:         currentDepth - previousDepth,
	}

	// Determine alert level
	if currentDepth > 1000 {
		payload.AlertLevel = "critical"
	} else if currentDepth > 500 {
		payload.AlertLevel = "warning"
	} else {
		payload.AlertLevel = "normal"
	}

	severity := SeverityInfo
	if payload.AlertLevel == "critical" {
		severity = SeverityError
	} else if payload.AlertLevel == "warning" {
		severity = SeverityWarning
	}

	event, err := NewEvent(EventQueueDepthChanged, severity, component, payload)
	if err != nil {
		return err
	}

	return e.Emit(event)
}

// EmitWorkerStatusChanged emits a worker status change event
func (e *Emitter) EmitWorkerStatusChanged(workerType, workerID, previousStatus, currentStatus string) error {
	payload := &WorkerEventPayload{
		WorkerType:     workerType,
		WorkerID:       workerID,
		PreviousStatus: previousStatus,
		CurrentStatus:  currentStatus,
		LastActivity:   time.Now(),
	}

	severity := SeverityInfo
	if currentStatus == "error" || currentStatus == "failed" {
		severity = SeverityError
	} else if currentStatus == "stopped" {
		severity = SeverityWarning
	}

	event, err := NewEvent(EventWorkerStatusChanged, severity, "worker", payload)
	if err != nil {
		return err
	}
	event.WorkerID = workerID

	return e.Emit(event)
}