  # Batch Processing Pipeline Monitoring Guide

## Overview

The monitoring system provides comprehensive visibility into the batch processing pipeline, tracking data flow from submission collection through final aggregation.

The system supports both P2PSnapshotSubmission batch format (multiple submissions per message) and single SnapshotSubmission format, with proper Redis key patterns for data organization.

## RESTful Monitor API

The monitoring system includes a complete RESTful API with 10 endpoints and interactive Swagger UI for comprehensive monitoring and debugging.

### Accessing the Monitor API

```bash
# Monitor API runs on port 9091 (configurable via MONITOR_API_PORT)
http://localhost:9091/swagger/index.html

# Interactive Swagger UI provides:
# - Complete API documentation
# - Interactive testing of all endpoints
# - Request/response examples
# - Protocol/market query parameter support
```

### All 10 Monitoring Endpoints

| Endpoint | Purpose | Query Parameters |
|----------|---------|-----------------|
| `/api/v1/health` | Service health check | None |
| `/api/v1/dashboard/summary` | Real-time dashboard metrics | protocol, market |
| `/api/v1/epochs/timeline` | Epoch progression timeline | protocol, market |
| `/api/v1/batches/finalized` | Recently finalized batches | protocol, market |
| `/api/v1/aggregation/results` | Network aggregation results | protocol, market |
| `/api/v1/timeline/recent` | Recent activity feed | protocol, market |
| `/api/v1/queues/status` | Queue monitoring | protocol, market |
| `/api/v1/pipeline/overview` | Pipeline status summary | protocol, market |
| `/api/v1/stats/daily` | Daily aggregated statistics | protocol, market |
| `/api/v1/stats/hourly` | Hourly performance metrics | protocol, market |

### Using Query Parameters

All endpoints support protocol/market filtering for multi-market environments:

```bash
# Filter by specific protocol and market
curl "http://localhost:9091/api/v1/dashboard/summary?protocol=powerloom&market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f"

# Multiple markets (comma-separated)
curl "http://localhost:9091/api/v1/batches/finalized?market=0x21cb57C1f2352ad215a463DD867b838749CD3b8f,0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c"

# JSON array format
curl "http://localhost:9091/api/v1/aggregation/results?market=[\"0x21cb57C1f2352ad215a463DD867b838749CD3b8f\",\"0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c\"]"
```

### Working Endpoints Status

All 10 endpoints are operational with real data:
- **Health Check**: Shows `data_fresh: true` with actual pipeline data
- **Dashboard**: Real metrics with participation rates, current epoch status
- **Epoch Timeline**: Actual epoch progression with correct status reading
- **Finalized Batches**: Batch data with validator attribution and IPFS CIDs
- **Aggregation Results**: Network-wide consensus with validator counting
- **Timeline Activity**: Recent submissions and batch completions
- **Queue Status**: Real-time queue depths and processing rates with accurate monitoring
- **Pipeline Overview**: Complete pipeline status with health indicators
- **Daily/Stats**: Actual aggregated data from pipeline metrics

### Queue Monitoring System

The queue monitoring system accurately reflects system health by monitoring active components.

#### Queue Monitoring Architecture
**Stream-Based System**:
- `stream:aggregation:notifications` - Primary operational queue
- Monitored via `getStreamLag()` for accurate consumer lag
- Handles real-time notifications efficiently

#### Queue Monitoring Commands:
```bash
# Check queue status
curl "http://localhost:9091/api/v1/queues/status" | jq '.aggregation_queue_depth'

# Monitor queue health history
curl "http://localhost:9091/api/v1/pipeline/overview" | jq '.queue_health_history'
```

### Key Features

- **Real-time Data**: All endpoints read from actual pipeline data sources
- **Proper Namespacing**: Uses `{protocol}:{market}:*` Redis keys correctly
- **Multi-market Support**: Handles comma-separated and JSON array market configurations
- **Production-safe**: Uses SCAN operations instead of KEYS
- **Validator Attribution**: Tracks which validators contributed to each batch
- **IPFS Integration**: Includes proper IPFS CIDs and Merkle roots in responses

### Monitoring Workflow

```bash
# 1. Check overall health
curl "http://localhost:9091/api/v1/health"

# 2. Get dashboard summary
curl "http://localhost:9091/api/v1/dashboard/summary"

# 3. View recent epochs
curl "http://localhost:9091/api/v1/epochs/timeline"

# 4. Check finalized batches
curl "http://localhost:9091/api/v1/batches/finalized"

# 5. Monitor network aggregation
curl "http://localhost:9091/api/v1/aggregation/results"
```

### Aggregator Metrics Counting

The aggregator metrics reporting system correctly counts finalized batches.

#### Technical Implementation:
```go
// Counts timeline entries with "aggregated:" prefix
timelineKey := a.keyBuilder.MetricsBatchesTimeline(protocol, market)
timelineEntries, err := a.redisClient.ZRange(a.ctx, timelineKey, 0, -1).Result()
for _, entry := range timelineEntries {
    if strings.HasPrefix(entry, "aggregated:") {
        aggregatedCount++
    }
}
```

#### Verification Commands:
```bash
# Check aggregator metrics
curl "http://localhost:9091/api/v1/aggregation/results" | jq '.finalized_batches'

# Monitor timeline entries directly
docker exec redis redis-cli ZRANGE "powerloom:powerloom-localnet:metrics:batches:timeline" 0 -1
```

### Queue Monitoring

The queue monitoring system provides accurate consumer lag metrics by monitoring Redis streams.

```bash
# Check queue status
curl "http://localhost:9091/api/v1/queues/status"
```

#### Key Features:
- Stream-based monitoring provides accurate consumer lag metrics
- Real-time visibility into actual processing performance

### Epoch ID Formatting

All epoch IDs display as integers for consistency across the system.

```bash
# API responses show consistent integer format
curl "http://localhost:9091/api/v1/aggregation/results?protocol=0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401&market=0xae32c4FA72E2e5F53ed4D214E4aD049286Ded16f"

# Expected output: "epoch_id": "23646205" (integer)
```

#### Epoch ID Handling:
- All components handle epoch ID formats consistently
- Epoch formatting utility in `pkgs/utils/epoch_formatter.go`
- Consistent integer display in all API responses

## Quick Start

### Basic Monitoring
```bash
# Check service status
./dsv.sh status

# View all logs
./dsv.sh logs

# View specific component logs
./dsv.sh aggregator-logs
./dsv.sh p2p-logs
./dsv.sh finalizer-logs
./dsv.sh dequeuer-logs
./dsv.sh event-logs
./dsv.sh redis-logs

# Access interactive dashboard
./dsv.sh dashboard

# Stream debugging commands
./dsv.sh stream-info
./dsv.sh stream-groups
./dsv.sh stream-dlq

# Queue management
./dsv.sh clean-queue
./dsv.sh clean-timeline
```

### Advanced Monitoring
```bash
# Check accurate queue health (stream-based)
curl "http://localhost:9091/api/v1/queues/status"

# Monitor real-time aggregation results
curl "http://localhost:9091/api/v1/aggregation/results"

# Check dashboard summary
curl "http://localhost:9091/api/v1/dashboard/summary"

# Verify system pipeline status
curl "http://localhost:9091/api/v1/pipeline/overview"

# Get timeline data
curl "http://localhost:9091/api/v1/epochs/timeline"

# View recent activity
curl "http://localhost:9091/api/v1/timeline/recent"

# Get daily stats
curl "http://localhost:9091/api/v1/stats/daily"
```

## Pipeline Stages

The monitoring system tracks 6 distinct stages:

### Stage 1: Submission Collection
- **Active Windows**: Open submission windows accepting data (format: `epoch:market:epochID:window`)
- **Queue Depth**: Pending submissions in processing queue
- **Vote Distribution**: Multiple CIDs per project with vote counts
- **Batch vs Single Submissions**: Now handles P2PSnapshotSubmission batch format

### Stage 2: Batch Splitting
- **Split Metadata**: How epochs are divided into parallel batches
- **Finalization Queue**: Batches waiting for processing
- **Batch Size**: Configurable via `FINALIZATION_BATCH_SIZE` (default: 20)

### Stage 3: Parallel Finalization
- **Worker Status**: Individual finalizer worker health
- **Batch Parts**: Processing status of each batch part
- **Worker Assignment**: Which worker handles which batch

### Stage 4: Aggregation
- **Ready Epochs**: Epochs with all parts completed
- **Aggregation Queue**: Epochs awaiting final assembly
- **Aggregator Status**: Single worker combining batch parts

### Stage 5: Final Output
- **Finalized Batches**: Completed batches with IPFS CIDs
- **Merkle Roots**: Cryptographic proofs of batch contents
- **Validator Broadcasts**: Vote dissemination status

### Stage 6: Consensus/Batch Aggregation (LIVE)
- **Validator Batch Exchange**: Real-time tracking of batch broadcasts between validators
- **Local Aggregation**: Each validator aggregates all received batches locally
- **Vote Counting**: Per-project CID vote tallies from all validators
- **Consensus Determination**: Local consensus based on majority vote per project
- **Decentralized Aggregation**: Each validator maintains their own local batch aggregation
- **No Global Consensus**: Independent local vote determination
- **Gossipsub Broadcast**: Batches shared via `/powerloom/finalized-batches/all`

## Monitoring Commands

### dsv.sh Commands

| Command | Description |
|---------|-------------|
| `status` | Service status overview |
| `logs [N]` | All service logs |
| `dashboard` | Open monitoring dashboard |
| **Component Logs** | |
| `p2p-logs [N]` | P2P Gateway activity |
| `aggregator-logs [N]` | Aggregator logs |
| `finalizer-logs [N]` | Finalizer logs |
| `dequeuer-logs [N]` | Dequeuer logs |
| `event-logs [N]` | Event monitor logs |
| `redis-logs [N]` | Redis operation logs |
| `ipfs-logs [N]` | IPFS node logs |

### Stream Debugging Commands
| Command | Description |
|---------|-------------|
| `stream-info` | Show Redis streams status |
| `stream-groups` | Show consumer groups |
| `stream-dlq` | Show dead letter queue |
| `stream-reset` | Reset streams (dangerous!) |

### Queue Management Commands
| Command | Description |
|---------|-------------|
| `clean-queue` | Clean up stale aggregation queue items |
| `clean-timeline` | Clean up timeline scientific notation entries |

All log commands support optional line count (default: 100):
```bash
./dsv.sh p2p-logs 200       # Last 200 lines
./dsv.sh aggregator-logs    # Default 100 lines
```

### Direct Script Usage

```bash
# Run comprehensive pipeline monitor
./scripts/monitor_pipeline.sh

# Specify custom container
./scripts/monitor_pipeline.sh container-name
```

## Monitoring Workflow

```bash
# 1. Check overall health
curl "http://localhost:9091/api/v1/health"

# 2. Get dashboard summary
curl "http://localhost:9091/api/v1/dashboard/summary"

# 3. View recent epochs
curl "http://localhost:9091/api/v1/epochs/timeline"

# 4. Check finalized batches
curl "http://localhost:9091/api/v1/batches/finalized"

# 5. Monitor network aggregation
curl "http://localhost:9091/api/v1/aggregation/results"

# 6. Check queue status
curl "http://localhost:9091/api/v1/queues/status"

# 7. View pipeline overview
curl "http://localhost:9091/api/v1/pipeline/overview"
```


## Redis Keys Structure

### Consensus/Aggregation Tracking
```
# Validator batch storage
validator:{validatorID}:batch:{epochID}              # Individual validator's batch for epoch
validator:{validatorID}:epoch:{epochID}:batch        # Alternative key pattern

# Aggregation status
consensus:epoch:{epochID}:status                     # Local aggregation status for epoch
consensus:epoch:{epochID}:result                     # Final local consensus determination

# Epoch validator tracking
epoch:{epochID}:validators                           # Set of validators who submitted batches
```

### Submission Tracking (Namespaced and Corrected)
```
# Submission Collection
submissionQueue                                     # Raw P2P submissions
{protocol}:{market}:processed:{sequencerID}:{id}    # Individual processed submissions (10min TTL)
{protocol}:{market}:epoch:{epochId}:processed       # Set of submission IDs per epoch (1hr TTL)
{protocol}:{market}:epoch:{epochId}:project:{pid}:votes # Vote counts per CID (fixed namespacing)

# New consensus tracking keys with protocol/market context
{protocol}:{market}:consensus:{epochId}:total_validators    # Total validators for epoch
{protocol}:{market}:consensus:{epochId}:voting_progress     # Overall consensus voting status
{protocol}:{market}:consensus:{epochId}:project:{pid}:votes # Per-project validator votes
{protocol}:{market}:consensus:{epochId}:project:{pid}:cid   # Winning CID for project
{protocol}:{market}:consensus:{epochId}:status              # Epoch consensus status

# Type-safe performance tracking keys
{protocol}:{market}:metrics:total_processed         # Total submissions processed per market
{protocol}:{market}:metrics:processing_rate         # Submissions per minute per market
```

**Key Features**:
- Uses {protocol}:{market} namespacing for all consensus and metric keys
- Ensures consistent namespacing across submission and consensus tracking
- Provides type-safe performance metrics
- Supports multi-market performance tracking

### Window Management
```
epoch:{market}:{epochId}:window                     # Active submission windows
{protocol}:{market}:batch:ready:{epochId}          # Ready batches for finalization
batch:finalized:{epochId}                          # Finalized batches with metadata
```

### Batch Processing
```
{protocol}:{market}:epoch:{epochId}:batch:meta      # Split batch metadata
{protocol}:{market}:finalizationQueue               # Queue of batch parts
batch:{epochId}:part:{partId}:status                # Part processing status
epoch:{epochId}:parts:completed                     # Completed parts count
epoch:{epochId}:parts:total                         # Total parts count

# Stream-Based Queue System
stream:aggregation:notifications                  # Primary operational queue
epoch:{epochId}:parts:ready                        # Ready flag for aggregation
```

### Epoch ID Handling
```
# Epoch IDs are properly formatted in all components:
# Format: Integer (23646205)

# Managed via FormatEpochID() utility:
pkgs/utils/epoch_formatter.go                      # Epoch formatting
All components handle epoch ID formats consistently
```

### Queue Monitoring Architecture
```
# Stream-Based System:
- stream:aggregation:notifications
- getStreamLag() function for accurate consumer lag tracking
- Real-time queue depth monitoring
```

### Worker Monitoring
```
worker:finalizer:{id}:status              # Worker current status
worker:finalizer:{id}:heartbeat           # Last heartbeat timestamp
worker:finalizer:{id}:current_batch       # Batch being processed
worker:finalizer:{id}:batches_processed   # Total processed count
worker:aggregator:status                  # Aggregator status
worker:aggregator:current_epoch           # Epoch being aggregated
```

### Performance Metrics
```
# Namespaced Multi-Market Metrics
{protocol}:{market}:metrics:total_processed     # Total submissions processed per market/protocol
{protocol}:{market}:metrics:processing_rate     # Submissions per minute per market/protocol
{protocol}:{market}:metrics:avg_latency         # Average processing time per market/protocol
{protocol}:{market}:metrics:validator_count     # Number of active validators per market/protocol
{protocol}:{market}:metrics:consensus_rate      # Percentage of epochs reaching consensus
```

**Performance Tracking Features**:
- Includes protocol and market context in all metrics
- Provides validator and consensus performance metrics
- Enables granular performance analysis across different markets and protocols
- Supports multi-market performance comparison

## Worker Status Values

| Status | Description |
|--------|-------------|
| `idle` | Worker available for tasks |
| `processing` | Actively processing batch |
| `failed` | Error state requiring attention |

## Consensus Status Values

| Status | Description |
|--------|-------------|
| `initializing` | Consensus process starting |
| `voting` | Collecting validator votes |
| `threshold_reached` | Consensus vote threshold achieved |
| `timeout` | Consensus voting window expired |
| `finalized` | Batch approved and ready |
| `rejected` | Batch did not meet consensus requirements

## Health Indicators

### Healthy Pipeline
- Queue depth < 100 (stream-based monitoring)
- Finalization queue < 10 batches
- Worker heartbeats < 60s old
- Aggregation queue < 5 epochs (stream-based)
- Regular validator batch exchange (>= 2 validators per epoch)
- Consensus determination completing (projects have majority votes)
- IPFS batch retrieval succeeding
- Queue monitoring shows accurate stream lag

### Queue Health Metrics
**Stream-Based Queue**:
- Stream lag < 100 (actual consumer lag)
- Processing rate consistent with throughput
- No unbounded accumulation

### Warning Signs
- Queue depth > 100 (stream-based backlog forming)
- Worker heartbeat > 60s (stale worker)
- Finalization queue > 10 (workers overwhelmed)
- Only 1 validator participating (no aggregation possible)
- High vote divergence (no clear majority for projects)
- IPFS retrieval failures for validator batches
- Stale validator batches (no recent broadcasts)

### Critical Issues
- Queue depth > 1000 (severe backlog)
- No worker heartbeats (workers down)
- Aggregation blocked (parts incomplete)
- No validator batch exchange (network partition)
- Validator batch signature validation failures
- Consistent IPFS failures (storage layer down)

## Integration with Workers

When implementing parallel workers, use the provided monitoring utilities:

```go
import "github.com/powerloom/snapshot-sequencer-validator/pkgs/workers"

// Initialize worker monitor
monitor := workers.NewWorkerMonitor(
    redisClient,
    "worker-1",
    workers.WorkerTypeFinalizer,
    "powerloom-localnet",
)

// Start worker with monitoring
monitor.StartWorker(ctx)

// Track processing
monitor.ProcessingStarted("epoch:123:batch:5")
// ... do work ...
monitor.ProcessingCompleted()

// Handle failures
if err != nil {
    monitor.ProcessingFailed(err)
}
```

## Performance Optimization

### Batch Size Tuning
Adjust `FINALIZATION_BATCH_SIZE` based on:
- Number of projects per epoch
- Available worker capacity
- Network latency requirements

```bash
# For high-volume epochs (500+ projects)
FINALIZATION_BATCH_SIZE=50

# For low-latency requirements
FINALIZATION_BATCH_SIZE=10
```

### Worker Scaling
Monitor queue depths to determine scaling needs:
- Queue > 100: Add more dequeuer workers
- Finalization queue > 10: Add finalizer workers
- Aggregation queue > 5: Optimize aggregator

## Troubleshooting

### No Active Windows
```bash
# Check event monitor is running
./dsv.sh event-logs

# Verify EVENT_START_BLOCK setting
grep EVENT_START_BLOCK .env
```

### Workers Not Visible
Workers only appear when implemented. Current status:
- Dequeuer workers (5 instances)
- Finalizer workers and aggregation worker are not yet implemented

### Queue Backlog
```bash
# Check dequeuer performance
./dsv.sh dequeuer-logs | grep "Processing submission"

# Monitor queue depth trends
curl "http://localhost:9091/api/v1/queues/status" | jq '.aggregation_queue_depth'
```

### Data Format Issues
The system handles both batch and single submission formats:
```bash
# Check for batch format submissions
./dsv.sh dequeuer-logs | grep "P2PSnapshotSubmission"

# Check field names (camelCase format)
./dsv.sh dequeuer-logs | grep "epochId\|projectId"

# Verify conversion strategy
docker exec powerloom-sequencer-validator-dequeuer-1 \
  printenv SUBMISSION_FORMAT_STRATEGY

# Manual format override
# Possible values: 'auto', 'single', 'batch'
SUBMISSION_FORMAT_STRATEGY=single ./dsv.sh distributed
```

### Epoch ID Format Issues
```bash
# Check epoch ID format in logs (should show integers)
./dsv.sh aggregator-logs | grep -E "epoch.*=" | tail -3
# Expected: "epoch=23646205"

# Verify epoch ID formatting in monitor API
curl "http://localhost:9091/api/v1/epochs/timeline" | jq '.epochs[0:2] | .[] | {epoch_id, status}'
# Expected: epoch_id should be integers (23646205)

# Check if FormatEpochID is being used
docker exec <aggregator-container> grep -A5 "FormatEpochID" /app/main.go
```

### Queue Monitoring Accuracy Issues
```bash
# Verify queue monitoring (shows stream lag)
curl "http://localhost:9091/api/v1/queues/status" | jq '.aggregation_queue_depth'
# Expected: Reasonable stream-based lag

# Check if getStreamLag function is being used
docker exec <monitor-container> grep -A10 "getStreamLag" /app/main.go
```

#### Queue Monitoring Troubleshooting:
- **Stream Lag**: Should be reasonable (< 100 for healthy system)
- **Monitor API**: Shows accurate stream-based metrics

#### Conversion Troubleshooting
- **Auto Strategy**: Automatically detects and converts submission formats
- **Single Strategy**: Forces single submission parsing
- **Batch Strategy**: Forces batch submission parsing

##### Common Conversion Mappings
| Field | Format |
|-------|--------|
| `epoch_id`     | `epochId`       |
| `project_id`   | `projectId`     |
| `market_id`    | `marketId`      |
| `submitter`    | `submitterAddress` |

**Warning Signs**:
- Logs showing parsing errors
- Submissions not being processed
- Inconsistent queue depths
- Metrics not being collected

##### Debugging Conversion
1. Check logs for detailed parsing information
2. Verify container environment variables
3. Test with explicit conversion strategy
4. Monitor metrics and queue processing

**Type Conversion Features**:
- Helper functions to handle uint64 → float64 JSON unmarshaling
- FinalizedBatches endpoint extracts:
  - Full IPFS CID
  - ValidatorCount
  - Complete submission details
- Consistent type conversion across API endpoints
- Improved getCurrentEpochInfo to find actual current epoch

### Stale Heartbeats
Indicates worker process issues:
1. Check container health
2. Review worker logs
3. Restart affected workers

## Advanced Monitoring

### Custom Metrics Collection
```bash
# Export metrics to file
docker exec sequencer-container redis-cli --csv HGETALL metrics:* > metrics.csv

# Real-time metric streaming
watch -n 1 'docker exec sequencer-container redis-cli GET metrics:processing_rate'
```

### Batch Tracking Query
```bash
# Find all batches for specific epoch
docker exec sequencer-container redis-cli KEYS "batch:123:*"

# Check specific batch part status
docker exec sequencer-container redis-cli GET "batch:123:part:5:status"
```

## Debugging Tips

### Using Combined Logs

The combined log commands are particularly useful for debugging cross-component issues:

1. **Collection Issues** (`collection-logs`):
   - Track submissions from P2P receipt through window collection
   - Verify dequeuer is storing submissions with correct keys
   - Confirm event monitor is finding stored submissions

2. **Finalization Issues** (`finalization-logs`):
   - Verify batches are pushed to finalization queue
   - Check if finalizers are picking up batches
   - Track consensus selection and batch creation

3. **Full Pipeline Issues** (`pipeline-logs`):
   - End-to-end submission tracking
   - Identify where submissions get stuck
   - Correlate timing between components

### Common Issues and Solutions

| Issue | Check With | Solution |
|-------|------------|----------|
| Submissions not collected | `event-logs` | Verify protocolState matches between components |
| Finalizers idle | `finalizer-logs` | Check finalization queue has batches |
| Missing projects | `aggregator-logs` | Ensure JSON parsing matches data structure |
| Queue buildup | `curl api/v1/queues/status` | Increase worker count or check for errors |

## Future Enhancements

Potential monitoring improvements:
- HTTP metrics endpoint (`/metrics`)
- Prometheus integration
- Grafana dashboards
- WebSocket real-time updates
- Historical trend analysis

### Consensus Troubleshooting

#### No Validator Batches Received
Check the aggregation results endpoint to see if validator batches are being received and processed:

```bash
# Check aggregation results for recent validator participation
curl "http://localhost:9091/api/v1/aggregation/results"

# Check Redis for validator presence in recent epochs
docker exec <container> redis-cli SMEMBERS "epoch:12345:validators"

# Check P2P Gateway logs for connectivity
./dsv.sh p2p-logs | grep -E "Connected|peer"
```

#### High Vote Divergence
Monitor the aggregation results to see vote distribution:

```bash
# Check aggregation results for vote distribution
curl "http://localhost:9091/api/v1/aggregation/results" | jq '.results[]'

# Look for projects with inconsistent CID votes across epochs
curl "http://localhost:9091/api/v1/batches/finalized" | jq '.batches[] | {epoch_id, projects}'
```

#### Validator Batches Not Aggregating
Check if the minimum validator threshold is being met:

```bash
# Check aggregation results for validator count
curl "http://localhost:9091/api/v1/aggregation/results"

# Check Redis for validator presence
docker exec <container> redis-cli SMEMBERS "epoch:12345:validators"

# Check aggregator logs for processing issues
./dsv.sh aggregator-logs | grep "AGGREGATION"
```

#### IPFS Batch Retrieval Failures
Check IPFS connectivity and retrieval:

```bash
# Check IPFS logs for connectivity issues
./dsv.sh ipfs-logs

# Check IPFS client configuration
docker exec <container> printenv | grep IPFS

# Test IPFS connectivity
docker exec <container> curl http://localhost:5001/api/v0/version
```

#### Network Issues
Monitor P2P connectivity and message passing:

```bash
# Check P2P Gateway logs for connectivity
./dsv.sh p2p-logs | grep -E "Connected|peer"

# Check gossipsub topic subscriptions
./dsv.sh p2p-logs | grep "gossipsub"

# Monitor aggregation processing
./dsv.sh aggregator-logs | grep "AGGREGATION"
```

### Consensus Monitoring Features
- Detailed validator performance tracking
- Stake-weighted voting visualization
- Cross-epoch validator reputation system
- Advanced consensus game theory modeling
- Automated validator onboarding/offboarding tools

## Related Documentation

- [Entity ID Reference Guide](../../specs/ENTITY_ID_REFERENCE_GUIDE.md) - Comprehensive entity ID format reference and usage guide
- [VPS_DEPLOYMENT.md](../VPS_DEPLOYMENT.md) - Deployment and operations guide
- [PHASE_2_CURRENT_WORK.md](../../PHASE_2_CURRENT_WORK.md) - Architecture overview
- [PROJECT_STATUS.md](../../PROJECT_STATUS.md) - Implementation status

## Entity ID Understanding

For detailed information about entity ID formats, metadata, and how to interpret monitoring data, see the [Entity ID Reference Guide](../../specs/ENTITY_ID_REFERENCE_GUIDE.md). This comprehensive guide explains:

- All entity ID formats (enhanced and legacy)
- Metadata extraction and usage
- Entity type descriptions and purposes
- Practical examples and parsing guides
- Troubleshooting and best practices

### Key Entity ID Types
- **Submissions**: `received:{epoch}:{slot}:{project}:{timestamp}:{peer}` - Format with rich context
- **Validations**: `{epoch}-baseSnapshot:{validator}:{market}:{slot}-{project}:{ipfs}` - Validator contributions
- **Batch Events**: `peer_discovery:{count}:{timestamp}`, `local:{epoch}`, `validator:{validator}:{epoch}`, `aggregated:{epoch}`
- **Epoch Events**: `open:{epoch}`, `close:{epoch}` - Window lifecycle events