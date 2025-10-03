package events

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

// ComponentWithEvents demonstrates how to integrate the event emitter into a component
type ComponentWithEvents struct {
	name       string
	emitter    *Emitter
	publisher  *Publisher
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewComponentWithEvents creates a new component with event support
func NewComponentWithEvents(name string, redisClient *redis.Client) (*ComponentWithEvents, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create emitter
	emitterConfig := &EmitterConfig{
		BufferSize:     500,
		MaxWorkers:     5,
		EventTimeout:   2 * time.Second,
		EnableMetrics:  true,
		DropOnOverflow: true,
		Protocol:       "prost_1b",
		DataMarket:     "0x123",
		SequencerID:    name,
	}
	emitter := NewEmitter(emitterConfig)

	// Create publisher if Redis is available
	var publisher *Publisher
	if redisClient != nil {
		pubConfig := &PublisherConfig{
			RedisClient:    redisClient,
			ChannelPrefix:  fmt.Sprintf("dsv:events:%s", name),
			BatchSize:      50,
			FlushInterval:  500 * time.Millisecond,
			EnableBatching: true,
			Protocol:       "prost_1b",
			DataMarket:     "0x123",
		}
		var err error
		publisher, err = NewPublisher(pubConfig)
		if err != nil {
			cancel()
			return nil, err
		}
	}

	return &ComponentWithEvents{
		name:      name,
		emitter:   emitter,
		publisher: publisher,
		ctx:       ctx,
		cancel:    cancel,
	}, nil
}

// Start initializes the component
func (c *ComponentWithEvents) Start() error {
	// Start emitter
	if err := c.emitter.Start(); err != nil {
		return fmt.Errorf("failed to start emitter: %w", err)
	}

	// Start publisher if available
	if c.publisher != nil {
		if err := c.publisher.Start(); err != nil {
			c.emitter.Stop()
			return fmt.Errorf("failed to start publisher: %w", err)
		}

		// Bridge emitter events to Redis
		c.bridgeToRedis()
	}

	// Subscribe to internal events
	c.subscribeToInternalEvents()

	log.Infof("Component %s started with event support", c.name)
	return nil
}

// Stop shuts down the component
func (c *ComponentWithEvents) Stop() error {
	c.cancel()

	// Stop publisher first
	if c.publisher != nil {
		c.publisher.Stop()
	}

	// Stop emitter
	c.emitter.Stop()

	log.Infof("Component %s stopped", c.name)
	return nil
}

// bridgeToRedis forwards local events to Redis
func (c *ComponentWithEvents) bridgeToRedis() {
	// Subscribe to all local events
	bridgeSubscriber := &Subscriber{
		ID: fmt.Sprintf("%s_redis_bridge", c.name),
		Handler: func(event *Event) {
			// Forward to Redis (non-blocking)
			go func() {
				if err := c.publisher.Publish(event); err != nil {
					log.WithError(err).Warn("Failed to publish event to Redis")
				}
			}()
		},
	}

	c.emitter.Subscribe(bridgeSubscriber)
}

// subscribeToInternalEvents sets up internal event handlers
func (c *ComponentWithEvents) subscribeToInternalEvents() {
	// Example: Log critical events
	criticalSubscriber := &Subscriber{
		ID: fmt.Sprintf("%s_critical_logger", c.name),
		Handler: func(event *Event) {
			if event.Severity == SeverityError {
				log.WithFields(log.Fields{
					"component": event.Component,
					"type":      event.Type,
					"epoch":     event.EpochID,
					"error":     event.Error,
				}).Error("Critical event occurred")
			}
		},
		Filter: func(event *Event) bool {
			return event.Severity == SeverityError
		},
	}

	c.emitter.Subscribe(criticalSubscriber)

	// Example: Track submission metrics
	submissionSubscriber := &Subscriber{
		ID: fmt.Sprintf("%s_submission_tracker", c.name),
		Types: []EventType{
			EventSubmissionReceived,
			EventSubmissionValidated,
			EventSubmissionRejected,
		},
		Handler: func(event *Event) {
			// Update internal metrics based on submission events
			switch event.Type {
			case EventSubmissionReceived:
				log.Debug("Submission received")
			case EventSubmissionValidated:
				log.Debug("Submission validated")
			case EventSubmissionRejected:
				log.Debug("Submission rejected")
			}
		},
	}

	c.emitter.Subscribe(submissionSubscriber)
}

// ProcessSubmission demonstrates event emission during submission processing
func (c *ComponentWithEvents) ProcessSubmission(submissionID, epochID, projectID string) error {
	startTime := time.Now()

	// Emit submission received event
	c.emitter.EmitSubmissionReceived(c.name, &SubmissionEventPayload{
		SubmissionID: submissionID,
		EpochID:      epochID,
		ProjectID:    projectID,
		SnapshotCID:  "QmXxx...",
	})

	// Simulate processing
	time.Sleep(10 * time.Millisecond)

	// Simulate validation
	valid := true // or false based on validation logic

	if valid {
		// Emit validation success
		event, _ := NewEvent(
			EventSubmissionValidated,
			SeverityInfo,
			c.name,
			&SubmissionEventPayload{
				SubmissionID: submissionID,
				EpochID:      epochID,
				ProjectID:    projectID,
			},
		)
		event.EpochID = epochID
		event.SubmissionID = submissionID
		event.Metadata["duration_ms"] = fmt.Sprintf("%d", time.Since(startTime).Milliseconds())

		c.emitter.Emit(event)
		return nil
	} else {
		// Emit validation failure
		event, _ := NewEvent(
			EventSubmissionRejected,
			SeverityWarning,
			c.name,
			&SubmissionEventPayload{
				SubmissionID: submissionID,
				EpochID:      epochID,
				ProjectID:    projectID,
				Reason:       "validation failed",
			},
		)
		event.EpochID = epochID
		event.SubmissionID = submissionID
		event.Error = "submission validation failed"

		c.emitter.Emit(event)
		return fmt.Errorf("submission validation failed")
	}
}

// ProcessEpoch demonstrates epoch lifecycle events
func (c *ComponentWithEvents) ProcessEpoch(epochID string) error {
	// Emit epoch started
	c.emitter.EmitEpochStarted(c.name, epochID)

	startTime := time.Now()

	// Simulate epoch processing
	time.Sleep(50 * time.Millisecond)

	// Emit epoch completed
	event, _ := NewEvent(
		EventEpochCompleted,
		SeverityInfo,
		c.name,
		&EpochEventPayload{
			EpochID:  epochID,
			EndTime:  time.Now().Unix(),
			Duration: time.Since(startTime).Milliseconds(),
		},
	)
	event.EpochID = epochID

	return c.emitter.Emit(event)
}

// MonitorQueue demonstrates queue monitoring events
func (c *ComponentWithEvents) MonitorQueue(queueName string, currentDepth int, previousDepth int) {
	// Emit queue depth change
	c.emitter.EmitQueueDepthChanged(c.name, queueName, currentDepth, previousDepth)

	// Check for critical thresholds
	if currentDepth > 1000 {
		event, _ := NewEvent(
			EventQueueDepthChanged,
			SeverityError,
			c.name,
			&QueueEventPayload{
				QueueName:    queueName,
				CurrentDepth: currentDepth,
				AlertLevel:   "critical",
				Threshold:    1000,
			},
		)
		event.Metadata["alert"] = "Queue depth critical"
		c.emitter.Emit(event)
	}
}

// GetMetrics returns component and event metrics
func (c *ComponentWithEvents) GetMetrics() map[string]interface{} {
	metrics := make(map[string]interface{})

	// Get emitter metrics
	emitterMetrics := c.emitter.GetMetrics()
	for k, v := range emitterMetrics {
		metrics[fmt.Sprintf("emitter_%s", k)] = v
	}

	// Get publisher metrics if available
	if c.publisher != nil {
		pubMetrics := c.publisher.GetMetrics()
		for k, v := range pubMetrics {
			metrics[fmt.Sprintf("publisher_%s", k)] = v
		}
	}

	return metrics
}

// Example usage in a real component like Dequeuer
func ExampleIntegration() {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Create component with events
	component, err := NewComponentWithEvents("dequeuer", redisClient)
	if err != nil {
		log.Fatal(err)
	}

	// Start component
	if err := component.Start(); err != nil {
		log.Fatal(err)
	}
	defer component.Stop()

	// Process submissions with automatic event emission
	component.ProcessSubmission("sub_1", "epoch_100", "project_A")
	component.ProcessSubmission("sub_2", "epoch_100", "project_B")

	// Process epoch
	component.ProcessEpoch("epoch_100")

	// Monitor queue
	component.MonitorQueue("submissionQueue", 150, 100)

	// Get metrics
	metrics := component.GetMetrics()
	log.WithField("metrics", metrics).Info("Component metrics")

	// Give time for async processing
	time.Sleep(100 * time.Millisecond)
}