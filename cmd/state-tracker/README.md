# State Tracker Service

The State Tracker service maintains the current and historical state of all entities in the DSV system. It tracks state transitions for epochs, submissions, and validators, providing efficient query APIs with deterministic aggregation capabilities.

## Features

- **State Management**: Tracks state transitions for epochs, submissions, and validators
- **Deterministic Aggregation**: Uses Redis set operations instead of expensive SCAN operations for improved performance
- **Efficient Indexing**: Uses Redis sorted sets, hashes, and sets for fast queries
- **Event-Driven**: Subscribes to events from the Event Emitter
- **REST API**: Provides comprehensive query endpoints
- **Prometheus Metrics**: Exports metrics for monitoring
- **Historical Data**: Maintains timeline of state changes
- **Backward Compatibility**: Graceful fallback mechanisms for older data formats

## State Transitions

### Epoch States
```
pending → processing → finalizing → aggregating → completed
```

### Submission States
```
received → validating → validated/rejected → finalized
```

### Validator States
```
offline → online → active → inactive
```

## API Endpoints

### Epoch Endpoints
- `GET /api/v1/state/epoch/{id}` - Get epoch state and metadata
- `GET /api/v1/state/epochs/active` - List active epochs
- `GET /api/v1/state/epochs/timeline` - Get epoch state transitions

### Submission Endpoints
- `GET /api/v1/state/submission/{id}` - Get submission state
- `GET /api/v1/state/submissions/active` - List active submissions
- `GET /api/v1/state/submissions/project/{projectId}` - Get submissions by project
- `GET /api/v1/state/submissions/snapshotter/{snapshotter}` - Get submissions by snapshotter

### Validator Endpoints
- `GET /api/v1/state/validator/{id}` - Get validator state
- `GET /api/v1/state/validators/active` - List active validators
- `GET /api/v1/state/validators/by-stake` - Get validators sorted by stake
- `GET /api/v1/state/validator/{id}/epochs` - Get validator's epoch participation
- `GET /api/v1/state/validator/{id}/batches` - Get validator's submitted batches

### Timeline Endpoint
- `GET /api/v1/state/timeline?type={epoch|submission|validator}&start={unix}&end={unix}` - Get state transition timeline

### Health Check
- `GET /api/v1/health` - Service health status

## Redis Index Structure

The service maintains efficient indexes with deterministic aggregation:

### New Deterministic Aggregation Keys
```
# Set Operations for Deterministic Aggregation (NEW - October 2025)
ActiveEpochs()                       # Set of currently active epoch IDs
EpochValidators({epochId})           # Set of validator IDs for each epoch
EpochProcessed({epochId})            # Set of processed submission IDs per epoch
```

### Traditional Index Structure (Backward Compatibility)
```
# Sorted Sets for Time-Based Queries
metrics:submissions:by_time          # Score: timestamp, Member: submission_id
metrics:epochs:by_time               # Score: timestamp, Member: epoch_id
metrics:finalizations:by_time        # Score: timestamp, Member: batch_id
validators:by_stake                  # Score: stake_amount, Member: validator_id

# Sets for Relationship Tracking
epoch:{epoch_id}:submissions         # Submission IDs in this epoch
epoch:{epoch_id}:validators          # Validator IDs in this epoch
validator:{id}:epochs                # Epochs this validator participated in
validator:{id}:batches               # Batches submitted by this validator
project:{id}:submissions             # Submissions for this project
snapshotter:{id}:submissions         # Submissions from this snapshotter

# Sets for State Tracking
pending:epochs                       # Epochs in pending state
active:epochs                        # Epochs being processed
completed:epochs                     # Completed epochs
active:submissions                   # Active submissions
rejected:submissions                 # Rejected submissions
finalized:submissions                # Finalized submissions
online:validators                    # Online validators
active:validators                    # Active validators
offline:validators                   # Offline validators

# Hashes for Current State
state:epoch:{epoch_id}               # Current state and metadata
state:submission:{id}                # Current state and metadata
state:validator:{id}                 # Current state and metadata
validator:{id}:stats                 # Aggregated statistics
validators:last_seen                 # Last seen timestamp for each validator
validators:peer_mapping              # Peer ID to validator ID mapping
```

## Configuration

Environment variables:
```bash
# Redis connection
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_PASSWORD=
REDIS_DB=0

# Service configuration
STATE_TRACKER_API_PORT=8085
STATE_TRACKER_METRICS_PORT=9094
STATE_TRACKER_RETENTION_DAYS=7

# Protocol configuration
PROTOCOL=aave
MARKET=mainnet

# Metrics integration
METRICS_ENABLED=true
METRICS_HOST=localhost
METRICS_PORT=8086

# Logging
LOG_LEVEL=info
```

## Running the Service

### Standalone
```bash
# Build
make state-tracker

# Run
./bin/state-tracker
```

### Docker
```bash
# Build image
docker build -f cmd/state-tracker/Dockerfile -t dsv-state-tracker .

# Run container
docker run -p 8085:8085 -p 9094:9094 \
  -e REDIS_HOST=redis \
  -e PROTOCOL=aave \
  -e MARKET=mainnet \
  dsv-state-tracker
```

### Docker Compose
```bash
# Start monitoring stack
docker-compose -f docker-compose.monitoring.yml up -d state-tracker
```

## Metrics

The service exports Prometheus metrics on port 9094:

- `state_tracker_transitions_total` - Total number of state transitions by entity type
- `state_tracker_active_entities` - Number of active entities by type
- `state_tracker_query_duration_seconds` - Duration of state queries

## Development

### Testing
```bash
# Run tests
go test ./cmd/state-tracker/...

# Test with race detection
go test -race ./cmd/state-tracker/...
```

### Example Queries

Get active epochs:
```bash
curl http://localhost:8085/api/v1/state/epochs/active
```

Get submission state:
```bash
curl http://localhost:8085/api/v1/state/submission/{submission_id}
```

Get validator statistics:
```bash
curl http://localhost:8085/api/v1/state/validator/{validator_id}
```

Get timeline for last hour:
```bash
curl "http://localhost:8085/api/v1/state/timeline?type=epoch&start=$(date -v-1H +%s)&end=$(date +%s)"
```

## Performance Improvements (October 2025)

### Deterministic Aggregation Benefits
- **Eliminated SCAN Operations**: Replaced inefficient SCAN operations with deterministic set operations
- **Reduced Redis Load**: Significant reduction in Redis round trips and CPU usage
- **Improved Scalability**: Better performance with high submission volumes and frequent epochs
- **Deterministic Behavior**: More reliable metrics aggregation without race conditions
- **Faster Response Times**: Direct key access provides quicker metric calculations

### Technical Implementation
- **ActiveEpochs Set**: Direct access to currently active epochs instead of timeline parsing
- **EpochValidators Sets**: Efficient validator detection per epoch instead of batch timeline iteration
- **EpochProcessed Sets**: Fast submission counting per epoch without expensive operations
- **Fallback Mechanisms**: Graceful degradation to timeline-based detection when new keys aren't available

### Backward Compatibility
- **No Breaking Changes**: All existing APIs continue to work without modification
- **Graceful Degradation**: Automatically falls back to timeline-based detection when needed
- **Data Format Consistency**: No changes to existing data structures or formats
- **Monitoring Continuity**: All monitoring endpoints provide consistent data

## Architecture Notes

1. **No SCAN Operations**: All queries use direct key access or set/sorted set operations
2. **Event-Driven Updates**: Subscribes to Redis Pub/Sub channels for real-time updates
3. **Atomic State Updates**: Uses Redis pipelines for atomic multi-key updates
4. **Automatic Cleanup**: Old state data is automatically removed after retention period
5. **Graceful Shutdown**: Properly closes connections and completes pending operations