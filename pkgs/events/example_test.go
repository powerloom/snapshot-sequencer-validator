package events_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/events"
)

// ExampleEmitter demonstrates basic event emitter usage
func ExampleEmitter() {
	// Create emitter configuration
	config := &events.EmitterConfig{
		BufferSize:     100,
		MaxWorkers:     5,
		EventTimeout:   2 * time.Second,
		EnableMetrics:  true,
		DropOnOverflow: true,
		Protocol:       "prost_1b",
		DataMarket:     "0x123...",
		SequencerID:    "validator_1",
	}

	// Create emitter
	emitter := events.NewEmitter(config)

	// Start emitter
	if err := emitter.Start(); err != nil {
		panic(err)
	}
	defer emitter.Stop()

	// Create a subscriber
	subscriber := &events.Subscriber{
		ID: "example_subscriber",
		Handler: func(event *events.Event) {
			fmt.Printf("Received event: %s\n", event.Type)
		},
		Types: []events.EventType{
			events.EventEpochStarted,
			events.EventSubmissionReceived,
		},
	}

	// Subscribe to events
	if err := emitter.Subscribe(subscriber); err != nil {
		panic(err)
	}

	// Emit some events
	emitter.EmitEpochStarted("example_component", "epoch_123")

	submission := &events.SubmissionEventPayload{
		SubmissionID: "sub_456",
		EpochID:      "epoch_123",
		ProjectID:    "project_789",
		SnapshotCID:  "QmXxx...",
	}
	emitter.EmitSubmissionReceived("example_component", submission)

	// Give handlers time to process
	time.Sleep(100 * time.Millisecond)
}

// ExamplePublisher demonstrates Redis publisher usage
func ExamplePublisher() {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	// Create publisher configuration
	config := &events.PublisherConfig{
		RedisClient:    redisClient,
		ChannelPrefix:  "dsv:events",
		BatchSize:      50,
		FlushInterval:  500 * time.Millisecond,
		EnableBatching: true,
		Protocol:       "prost_1b",
		DataMarket:     "0x123...",
	}

	// Create publisher
	publisher, err := events.NewPublisher(config)
	if err != nil {
		panic(err)
	}

	// Start publisher
	if err := publisher.Start(); err != nil {
		panic(err)
	}
	defer publisher.Stop()

	// Create event
	event, _ := events.NewEvent(
		events.EventWindowOpened,
		events.SeverityInfo,
		"monitor",
		&events.WindowEventPayload{
			EpochID:  "epoch_123",
			Status:   "open",
			OpenedAt: time.Now().Unix(),
		},
	)

	// Publish event
	if err := publisher.Publish(event); err != nil {
		fmt.Printf("Failed to publish: %v\n", err)
	}
}

// ExampleEventStream demonstrates event streaming
func ExampleEventStream() {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	// Create publisher
	config := events.DefaultPublisherConfig(redisClient)
	publisher, _ := events.NewPublisher(config)
	publisher.Start()
	defer publisher.Stop()

	// Create event stream for specific event types
	stream, err := publisher.NewEventStream(
		[]events.EventType{
			events.EventSubmissionValidated,
			events.EventSubmissionRejected,
		},
		func(event *events.Event) bool {
			// Filter only events from specific component
			return event.Component == "dequeuer"
		},
	)
	if err != nil {
		panic(err)
	}
	defer stream.Close()

	// Process events from stream
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for {
		select {
		case event := <-stream.Events():
			if event == nil {
				return // Stream closed
			}
			fmt.Printf("Streamed event: %s from %s\n", event.Type, event.Component)
		case <-ctx.Done():
			return
		}
	}
}

// TestEmitterConcurrency tests concurrent event emission
func TestEmitterConcurrency(t *testing.T) {
	config := events.DefaultConfig()
	emitter := events.NewEmitter(config)

	if err := emitter.Start(); err != nil {
		t.Fatal(err)
	}
	defer emitter.Stop()

	// Counter for received events
	received := make(chan int, 100)

	// Create subscriber
	subscriber := &events.Subscriber{
		ID: "test_subscriber",
		Handler: func(event *events.Event) {
			select {
			case received <- 1:
			default:
			}
		},
	}

	if err := emitter.Subscribe(subscriber); err != nil {
		t.Fatal(err)
	}

	// Emit 100 events concurrently
	for i := 0; i < 100; i++ {
		go func(i int) {
			event, _ := events.NewEvent(
				events.EventSubmissionReceived,
				events.SeverityInfo,
				"test",
				map[string]interface{}{"index": i},
			)
			emitter.Emit(event)
		}(i)
	}

	// Wait for all events to be received
	count := 0
	timeout := time.After(5 * time.Second)
	for count < 100 {
		select {
		case <-received:
			count++
		case <-timeout:
			t.Fatalf("Timeout: only received %d events", count)
		}
	}
}

// TestEventBufferOverflow tests buffer overflow handling
func TestEventBufferOverflow(t *testing.T) {
	config := &events.EmitterConfig{
		BufferSize:     10,
		MaxWorkers:     1,
		EventTimeout:   1 * time.Second,
		DropOnOverflow: true,
	}

	emitter := events.NewEmitter(config)
	if err := emitter.Start(); err != nil {
		t.Fatal(err)
	}
	defer emitter.Stop()

	// Slow subscriber
	subscriber := &events.Subscriber{
		ID: "slow_subscriber",
		Handler: func(event *events.Event) {
			time.Sleep(100 * time.Millisecond)
		},
	}

	if err := emitter.Subscribe(subscriber); err != nil {
		t.Fatal(err)
	}

	// Emit more events than buffer can hold
	for i := 0; i < 20; i++ {
		event, _ := events.NewEvent(
			events.EventQueueDepthChanged,
			events.SeverityInfo,
			"test",
			map[string]interface{}{"depth": i},
		)
		emitter.Emit(event)
	}

	// Check metrics
	metrics := emitter.GetMetrics()
	if metrics["events_dropped"].(uint64) == 0 {
		t.Error("Expected some events to be dropped due to buffer overflow")
	}
}