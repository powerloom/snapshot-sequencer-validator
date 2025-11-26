# State Tracker Worker

The State Tracker Worker is a **background service** that prepares pre-aggregated datasets for the Monitor API. It does **NOT** provide REST endpoints - it only runs background aggregation workers and exports Prometheus metrics.

## Primary Functionality

- **Dataset Preparation**: Pre-aggregates dashboard data, statistics, and metrics for Monitor API consumption
- **Event Processing**: Subscribes to Redis pub/sub channels to count state changes
- **Periodic Aggregation**: Runs automated workers to prepare hourly, daily, and metrics summaries
- **Data Pruning**: Automatically cleans up old timeline data to maintain Redis performance
- **Participation Metrics**: Calculates validator participation and inclusion rates
- **Current Epoch Status**: Tracks current epoch phase and timing information

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

## Data Outputs (For Monitor API)

The worker prepares these datasets that are consumed by the Monitor API:

### Dashboard Summary
- **`{protocol}:{market}:dashboard:summary`** - Current metrics with 1m/5m rates
  - submissions_total, submissions_queue, processed_submissions
  - epochs_total, batches_total, active_validators
  - submission_rate, epoch_rate, batch_rate
  - epochs_1m, batches_1m, submissions_1m (recent activity)

### Current Epoch Status
- **`{protocol}:{market}:metrics:current_epoch`** - Current epoch state
  - epoch_id, phase, time_remaining_seconds
  - submissions_received, window_duration

### Participation Metrics
- **`{protocol}:{market}:metrics:participation`** - Validator participation rates
  - epochs_participated_24h, participation_rate
  - level1_batches_24h, level2_inclusions_24h, inclusion_rate

### Statistics (Auto-expiring)
- **`{protocol}:{market}:stats:current`** - Hourly stats (60s TTL)
- **`{protocol}:{market}:stats:hourly:{timestamp}`** - Per-hour breakdown
- **`{protocol}:{market}:stats:daily`** - 24-hour summary

### Redis Keys Prepared
- Uses **deterministic aggregation** with `ActiveEpochs()`, `EpochValidators({epochId})`, `EpochSubmissionsIds({epochId})` ZSET
- Maintains timeline data for epochs, batches, and submissions
- Prunes data older than 24 hours automatically

## Redis Index Structure

The service maintains efficient indexes with deterministic aggregation:

### New Deterministic Aggregation Keys
```
# Set Operations for Deterministic Aggregation (NEW - October 2025)
ActiveEpochs()                       # Set of currently active epoch IDs
EpochValidators({epochId})           # Set of validator IDs for each epoch
EpochSubmissionsIds({epochId})       # ZSET of processed submission IDs per epoch (deterministic, ordered by timestamp)
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

# Service configuration (API port NOT used - only metrics)
STATE_TRACKER_METRICS_PORT=9094

# Protocol configuration
PROTOCOL_STATE_CONTRACT=your_contract_address
DATA_MARKET_ADDRESSES="market1_address"

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

# Run container (only metrics port needed)
docker run -p 9094:9094 \
  -e REDIS_HOST=redis \
  -e PROTOCOL_STATE_CONTRACT=your_contract \
  -e DATA_MARKET_ADDRESSES=your_market \
  dsv-state-tracker
```

### Docker Compose
```bash
# Start monitoring stack
docker-compose -f docker-compose.monitoring.yml up -d state-tracker
```

## Metrics

The service exports Prometheus metrics on port 9094:

- `state_tracker_changes_processed_total` - Total number of state changes by entity type
- `state_tracker_datasets_generated_total` - Total number of datasets generated
- `state_tracker_aggregation_duration_seconds` - Duration of aggregation operations

## Development

### Testing
```bash
# Run tests
go test ./cmd/state-tracker/...

# Test with race detection
go test -race ./cmd/state-tracker/...
```

### Example Queries

Check metrics (Prometheus format):
```bash
curl http://localhost:9094/metrics
```

Check dashboard summary (via Monitor API):
```bash
curl http://localhost:8086/api/v1/dashboard/summary
```

Check current epoch status (via Monitor API):
```bash
curl http://localhost:8086/api/v1/current-epoch
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
- **EpochSubmissionsIds ZSET**: Fast submission counting per epoch using `ZCARD` on deterministic ZSET
- **Fallback Mechanisms**: Graceful degradation to timeline-based detection when new keys aren't available

### Backward Compatibility
- **No Breaking Changes**: All existing APIs continue to work without modification
- **Graceful Degradation**: Automatically falls back to timeline-based detection when needed
- **Data Format Consistency**: No changes to existing data structures or formats
- **Monitoring Continuity**: All monitoring endpoints provide consistent data

## Architecture Notes

1. **Background Worker Only**: No HTTP API server - only metrics and Redis data preparation
2. **Event Processing**: Subscribes to Redis Pub/Sub "state:change" channel for real-time counting
3. **Multiple Aggregation Workers**: Runs concurrent workers for different time periods (metrics, hourly, daily, pruning)
4. **Monitor API Integration**: Data is consumed by Monitor API, not directly by users
5. **Deterministic Aggregation**: Uses ActiveEpochs(), EpochValidators(), and EpochSubmissionsIds() ZSET
6. **Graceful Shutdown**: Properly closes connections and stops all workers