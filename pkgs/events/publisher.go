package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

const (
	// DefaultEventChannel is the default Redis channel for events
	DefaultEventChannel = "dsv:events"

	// DefaultBatchSize is the default batch size for publishing
	DefaultBatchSize = 100

	// DefaultFlushInterval is the default interval for flushing batched events
	DefaultFlushInterval = 1 * time.Second
)

// PublisherConfig contains configuration for the Redis publisher
type PublisherConfig struct {
	RedisClient    *redis.Client
	ChannelPrefix  string        // Prefix for Redis channels
	BatchSize      int           // Number of events to batch before publishing
	FlushInterval  time.Duration // Maximum time to wait before flushing
	EnableBatching bool          // Enable event batching
	Protocol       string        // Protocol state for channel naming
	DataMarket     string        // Data market for channel naming
}

// DefaultPublisherConfig returns a default publisher configuration
func DefaultPublisherConfig(redisClient *redis.Client) *PublisherConfig {
	return &PublisherConfig{
		RedisClient:    redisClient,
		ChannelPrefix:  DefaultEventChannel,
		BatchSize:      DefaultBatchSize,
		FlushInterval:  DefaultFlushInterval,
		EnableBatching: true,
	}
}

// Publisher handles publishing events to Redis Pub/Sub
type Publisher struct {
	config      *PublisherConfig
	redisClient *redis.Client

	// Batching
	batch      []*Event
	batchMutex sync.Mutex
	flushTimer *time.Timer

	// Channel mapping for event types
	channelMap map[EventType]string

	// Metrics
	eventsPublished uint64
	batchesPublished uint64
	publishErrors   uint64

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	running bool
	mutex   sync.RWMutex
}

// NewPublisher creates a new Redis event publisher
func NewPublisher(config *PublisherConfig) (*Publisher, error) {
	if config == nil || config.RedisClient == nil {
		return nil, fmt.Errorf("invalid publisher configuration")
	}

	ctx, cancel := context.WithCancel(context.Background())

	publisher := &Publisher{
		config:      config,
		redisClient: config.RedisClient,
		batch:       make([]*Event, 0, config.BatchSize),
		channelMap:  make(map[EventType]string),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize channel mappings
	publisher.initChannelMap()

	return publisher, nil
}

// initChannelMap sets up the mapping from event types to Redis channels
func (p *Publisher) initChannelMap() {
	prefix := p.config.ChannelPrefix

	// Create specific channels for different event types
	p.channelMap[EventEpochStarted] = fmt.Sprintf("%s:epoch", prefix)
	p.channelMap[EventEpochCompleted] = fmt.Sprintf("%s:epoch", prefix)

	p.channelMap[EventSubmissionReceived] = fmt.Sprintf("%s:submission", prefix)
	p.channelMap[EventSubmissionValidated] = fmt.Sprintf("%s:submission", prefix)
	p.channelMap[EventSubmissionRejected] = fmt.Sprintf("%s:submission", prefix)

	p.channelMap[EventWindowOpened] = fmt.Sprintf("%s:window", prefix)
	p.channelMap[EventWindowClosed] = fmt.Sprintf("%s:window", prefix)

	p.channelMap[EventFinalizationStarted] = fmt.Sprintf("%s:finalization", prefix)
	p.channelMap[EventFinalizationCompleted] = fmt.Sprintf("%s:finalization", prefix)
	p.channelMap[EventPartCompleted] = fmt.Sprintf("%s:finalization", prefix)

	p.channelMap[EventAggregationLevel1Started] = fmt.Sprintf("%s:aggregation", prefix)
	p.channelMap[EventAggregationLevel1Completed] = fmt.Sprintf("%s:aggregation", prefix)
	p.channelMap[EventAggregationLevel2Started] = fmt.Sprintf("%s:aggregation", prefix)
	p.channelMap[EventAggregationLevel2Completed] = fmt.Sprintf("%s:aggregation", prefix)

	p.channelMap[EventValidatorBatchReceived] = fmt.Sprintf("%s:validator", prefix)

	p.channelMap[EventIPFSStorageCompleted] = fmt.Sprintf("%s:storage", prefix)

	p.channelMap[EventQueueDepthChanged] = fmt.Sprintf("%s:queue", prefix)

	p.channelMap[EventWorkerStatusChanged] = fmt.Sprintf("%s:worker", prefix)
}

// Start begins the publisher
func (p *Publisher) Start() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.running {
		return fmt.Errorf("publisher already running")
	}

	p.running = true
	log.Info("Starting Redis event publisher")

	// Start flush worker if batching is enabled
	if p.config.EnableBatching {
		p.wg.Add(1)
		go p.flushWorker()
	}

	return nil
}

// Stop gracefully shuts down the publisher
func (p *Publisher) Stop() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.running {
		return fmt.Errorf("publisher not running")
	}

	p.running = false
	log.Info("Stopping Redis event publisher")

	// Cancel context
	p.cancel()

	// Flush any remaining events
	if p.config.EnableBatching {
		p.flushBatch()
	}

	// Wait for workers to finish
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("Redis event publisher stopped gracefully")
	case <-time.After(5 * time.Second):
		log.Warn("Redis event publisher shutdown timeout")
	}

	return nil
}

// Publish sends an event to Redis
func (p *Publisher) Publish(event *Event) error {
	p.mutex.RLock()
	if !p.running {
		p.mutex.RUnlock()
		return fmt.Errorf("publisher not running")
	}
	p.mutex.RUnlock()

	if p.config.EnableBatching {
		return p.addToBatch(event)
	}

	return p.publishSingle(event)
}

// PublishBatch publishes multiple events at once
func (p *Publisher) PublishBatch(events []*Event) error {
	p.mutex.RLock()
	if !p.running {
		p.mutex.RUnlock()
		return fmt.Errorf("publisher not running")
	}
	p.mutex.RUnlock()

	// Group events by channel
	channelEvents := make(map[string][]*Event)
	for _, event := range events {
		channel := p.getChannel(event.Type)
		channelEvents[channel] = append(channelEvents[channel], event)
	}

	// Publish to each channel using pipeline
	pipe := p.redisClient.Pipeline()
	for channel, evts := range channelEvents {
		for _, evt := range evts {
			data, err := evt.ToJSON()
			if err != nil {
				log.WithError(err).Error("Failed to serialize event")
				p.publishErrors++
				continue
			}
			pipe.Publish(p.ctx, channel, string(data))
		}
	}

	_, err := pipe.Exec(p.ctx)
	if err != nil {
		p.publishErrors++
		return fmt.Errorf("failed to publish batch: %w", err)
	}

	p.eventsPublished += uint64(len(events))
	p.batchesPublished++

	return nil
}

// addToBatch adds an event to the current batch
func (p *Publisher) addToBatch(event *Event) error {
	p.batchMutex.Lock()
	defer p.batchMutex.Unlock()

	p.batch = append(p.batch, event)

	// Reset flush timer
	if p.flushTimer != nil {
		p.flushTimer.Stop()
	}
	p.flushTimer = time.AfterFunc(p.config.FlushInterval, func() {
		p.flushBatch()
	})

	// Check if batch is full
	if len(p.batch) >= p.config.BatchSize {
		return p.flushBatchLocked()
	}

	return nil
}

// flushBatch flushes the current batch
func (p *Publisher) flushBatch() error {
	p.batchMutex.Lock()
	defer p.batchMutex.Unlock()

	return p.flushBatchLocked()
}

// flushBatchLocked flushes the batch (assumes lock is held)
func (p *Publisher) flushBatchLocked() error {
	if len(p.batch) == 0 {
		return nil
	}

	// Copy batch for publishing
	events := make([]*Event, len(p.batch))
	copy(events, p.batch)

	// Clear batch
	p.batch = p.batch[:0]

	// Stop timer if running
	if p.flushTimer != nil {
		p.flushTimer.Stop()
		p.flushTimer = nil
	}

	// Publish batch
	go func() {
		if err := p.PublishBatch(events); err != nil {
			log.WithError(err).Error("Failed to publish event batch")
		}
	}()

	return nil
}

// publishSingle publishes a single event immediately
func (p *Publisher) publishSingle(event *Event) error {
	channel := p.getChannel(event.Type)
	data, err := event.ToJSON()
	if err != nil {
		p.publishErrors++
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	if err := p.redisClient.Publish(p.ctx, channel, string(data)).Err(); err != nil {
		p.publishErrors++
		return fmt.Errorf("failed to publish event: %w", err)
	}

	p.eventsPublished++
	return nil
}

// getChannel returns the Redis channel for an event type
func (p *Publisher) getChannel(eventType EventType) string {
	if channel, ok := p.channelMap[eventType]; ok {
		// Add protocol and market if configured
		if p.config.Protocol != "" && p.config.DataMarket != "" {
			return fmt.Sprintf("%s:%s:%s", p.config.Protocol, p.config.DataMarket, channel)
		}
		return channel
	}

	// Default channel
	return p.config.ChannelPrefix
}

// flushWorker periodically flushes batched events
func (p *Publisher) flushWorker() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.flushBatch()
		case <-p.ctx.Done():
			return
		}
	}
}

// GetMetrics returns publisher metrics
func (p *Publisher) GetMetrics() map[string]interface{} {
	p.batchMutex.Lock()
	batchSize := len(p.batch)
	p.batchMutex.Unlock()

	return map[string]interface{}{
		"events_published":  p.eventsPublished,
		"batches_published": p.batchesPublished,
		"publish_errors":    p.publishErrors,
		"current_batch_size": batchSize,
		"running":           p.running,
	}
}

// Subscribe creates a subscription to event channels
func (p *Publisher) Subscribe(ctx context.Context, eventTypes []EventType) (<-chan *Event, error) {
	// Create channels to subscribe to
	channels := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		channel := p.getChannel(eventType)
		channels = append(channels, channel)
	}

	// If no specific types, subscribe to all
	if len(channels) == 0 {
		channels = append(channels, fmt.Sprintf("%s:*", p.config.ChannelPrefix))
	}

	// Create pubsub
	pubsub := p.redisClient.PSubscribe(ctx, channels...)

	// Create event channel
	eventChan := make(chan *Event, 100)

	// Start goroutine to process messages
	go func() {
		defer close(eventChan)
		defer pubsub.Close()

		ch := pubsub.Channel()
		for {
			select {
			case msg := <-ch:
				if msg == nil {
					return
				}

				// Parse event from message
				var event Event
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					log.WithError(err).Warn("Failed to parse event from Redis")
					continue
				}

				// Send to channel
				select {
				case eventChan <- &event:
				case <-ctx.Done():
					return
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

// EventStream provides a stream of events with filtering
type EventStream struct {
	publisher  *Publisher
	filter     EventFilter
	eventTypes []EventType
	eventChan  chan *Event
	cancel     context.CancelFunc
}

// NewEventStream creates a new event stream
func (p *Publisher) NewEventStream(eventTypes []EventType, filter EventFilter) (*EventStream, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Subscribe to Redis channels
	redisChan, err := p.Subscribe(ctx, eventTypes)
	if err != nil {
		cancel()
		return nil, err
	}

	// Create buffered channel for filtered events
	eventChan := make(chan *Event, 100)

	stream := &EventStream{
		publisher:  p,
		filter:     filter,
		eventTypes: eventTypes,
		eventChan:  eventChan,
		cancel:     cancel,
	}

	// Start filtering goroutine
	go func() {
		defer close(eventChan)

		for event := range redisChan {
			// Apply filter if present
			if filter != nil && !filter(event) {
				continue
			}

			// Send to stream channel
			select {
			case eventChan <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return stream, nil
}

// Events returns the event channel
func (s *EventStream) Events() <-chan *Event {
	return s.eventChan
}

// Close closes the event stream
func (s *EventStream) Close() {
	s.cancel()
}