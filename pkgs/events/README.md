# Events Package

The events package provides a production-ready event emitter and publisher system for the DSV monitoring infrastructure. It supports async event processing, Redis Pub/Sub integration, and comprehensive metrics collection.

## Features

- **Async Event Processing**: Non-blocking event emission with buffered channels
- **Thread-Safe Operation**: Concurrent-safe emission and subscription handling
- **Redis Pub/Sub Integration**: Stream events across distributed components
- **Event Types**: Comprehensive event types for the entire pipeline
- **Buffer Management**: Configurable buffers with overflow handling
- **Metrics Collection**: Built-in metrics for monitoring event flow
- **Graceful Shutdown**: Proper cleanup and event draining on shutdown

## Event Types

The package supports the following event categories:

### Epoch Events
- `epoch_started`: New epoch begins
- `epoch_completed`: Epoch processing finished

### Submission Events
- `submission_received`: New submission from P2P network
- `submission_validated`: Submission passed validation
- `submission_rejected`: Submission failed validation

### Window Events
- `window_opened`: Submission window opened for epoch
- `window_closed`: Submission window closed

### Finalization Events
- `finalization_started`: Batch finalization begins
- `finalization_completed`: Batch finalization done
- `part_completed`: Worker completed batch part

### Aggregation Events
- `aggregation_level1_started`: Local aggregation begins
- `aggregation_level1_completed`: Local aggregation done
- `aggregation_level2_started`: Network aggregation begins
- `aggregation_level2_completed`: Network aggregation done

### Other Events
- `validator_batch_received`: Batch from remote validator
- `ipfs_storage_completed`: IPFS storage operation done
- `queue_depth_changed`: Queue depth threshold crossed
- `worker_status_changed`: Worker state transition

## Usage

### Basic Event Emitter

```go
// Create emitter configuration
config := &events.EmitterConfig{
    BufferSize:     1000,
    MaxWorkers:     10,
    EnableMetrics:  true,
    DropOnOverflow: true,
    Protocol:       "prost_1b",
    DataMarket:     "0x123...",
    SequencerID:    "validator_1",
}

// Create and start emitter
emitter := events.NewEmitter(config)
if err := emitter.Start(); err != nil {
    log.Fatal(err)
}
defer emitter.Stop()

// Subscribe to events
subscriber := &events.Subscriber{
    ID: "my_subscriber",
    Handler: func(event *events.Event) {
        log.Printf("Event: %s", event.Type)
    },
    Types: []events.EventType{
        events.EventEpochStarted,
        events.EventSubmissionReceived,
    },
}
emitter.Subscribe(subscriber)

// Emit events
emitter.EmitEpochStarted("dequeuer", "epoch_123")
```

### Redis Publisher

```go
// Create Redis publisher
config := &events.PublisherConfig{
    RedisClient:    redisClient,
    ChannelPrefix:  "dsv:events",
    BatchSize:      100,
    FlushInterval:  1 * time.Second,
    EnableBatching: true,
}

publisher, _ := events.NewPublisher(config)
publisher.Start()
defer publisher.Stop()

// Publish events
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
publisher.Publish(event)
```

### Event Streaming

```go
// Create event stream with filtering
stream, _ := publisher.NewEventStream(
    []events.EventType{
        events.EventSubmissionValidated,
        events.EventSubmissionRejected,
    },
    func(event *events.Event) bool {
        return event.Component == "dequeuer"
    },
)
defer stream.Close()

// Process events
for event := range stream.Events() {
    log.Printf("Received: %s", event.Type)
}
```

## Integration with Components

### Dequeuer Integration

```go
func (d *Dequeuer) processSubmission(sub *Submission) {
    // Emit submission received event
    d.emitter.EmitSubmissionReceived("dequeuer", &events.SubmissionEventPayload{
        SubmissionID: sub.ID,
        EpochID:      sub.EpochID,
        ProjectID:    sub.ProjectID,
        SnapshotCID:  sub.CID,
    })

    // Process submission...

    if valid {
        // Emit validation success
        d.emitter.Emit(&events.Event{
            Type:      events.EventSubmissionValidated,
            Severity:  events.SeverityInfo,
            Component: "dequeuer",
            EpochID:   sub.EpochID,
        })
    } else {
        // Emit validation failure
        d.emitter.Emit(&events.Event{
            Type:      events.EventSubmissionRejected,
            Severity:  events.SeverityWarning,
            Component: "dequeuer",
            Error:     "validation failed",
        })
    }
}
```

### Monitor Integration

```go
func (m *Monitor) handleQueueDepth(queueName string, depth int) {
    // Emit queue depth change
    m.emitter.EmitQueueDepthChanged(
        "monitor",
        queueName,
        depth,
        m.lastDepth[queueName],
    )

    m.lastDepth[queueName] = depth
}
```

## Configuration

### EmitterConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| BufferSize | int | 1000 | Size of event buffer channel |
| MaxWorkers | int | 10 | Maximum concurrent event processors |
| EventTimeout | Duration | 5s | Timeout for event handler execution |
| EnableMetrics | bool | true | Enable metrics collection |
| DropOnOverflow | bool | true | Drop events when buffer full |
| Protocol | string | - | Protocol state identifier |
| DataMarket | string | - | Data market address |
| SequencerID | string | - | Sequencer/validator ID |

### PublisherConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| RedisClient | *redis.Client | - | Redis client instance |
| ChannelPrefix | string | "dsv:events" | Prefix for Redis channels |
| BatchSize | int | 100 | Events per batch |
| FlushInterval | Duration | 1s | Maximum time before flush |
| EnableBatching | bool | true | Enable event batching |

## Metrics

The emitter provides the following metrics:

- `events_emitted`: Total events emitted
- `events_dropped`: Events dropped due to overflow
- `events_processed`: Events successfully processed
- `processing_errors`: Handler execution errors
- `buffer_usage`: Current buffer utilization
- `subscribers`: Number of active subscribers

Access metrics via:

```go
metrics := emitter.GetMetrics()
log.Printf("Events processed: %d", metrics["events_processed"])
```

## Best Practices

1. **Buffer Sizing**: Set buffer size based on expected event rate
2. **Worker Pool**: Adjust workers based on handler complexity
3. **Error Handling**: Always handle errors in event handlers
4. **Filtering**: Use type-specific subscriptions to reduce overhead
5. **Batching**: Enable batching for high-volume Redis publishing
6. **Graceful Shutdown**: Always call Stop() to drain pending events

## Thread Safety

All public methods are thread-safe and can be called concurrently. The package uses:
- Mutex protection for subscriber management
- Atomic operations for metrics
- Channel-based event distribution
- Worker pool for concurrent processing

## Performance Considerations

- Event emission is non-blocking when `DropOnOverflow` is true
- Use filtering to reduce processing overhead
- Batch Redis publishes for high-throughput scenarios
- Monitor metrics to detect buffer overflow issues