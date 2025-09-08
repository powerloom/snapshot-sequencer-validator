# Batch Processing Pipeline Monitoring Guide

## Overview

The enhanced monitoring system provides comprehensive visibility into the parallel batch processing pipeline, tracking data flow from submission collection through final aggregation.

The system now supports both P2PSnapshotSubmission batch format (multiple submissions per message) and single SnapshotSubmission format, with updated Redis key patterns for better organization.

## Quick Start

### Basic Monitoring
```bash
# Simple batch status monitoring
./launch.sh monitor

# Comprehensive pipeline monitoring (recommended)
./launch.sh pipeline
```

## Pipeline Stages

The monitoring system tracks 5 distinct stages:

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

## Monitoring Commands

### launch.sh Commands

| Command | Description |
|---------|-------------|
| `monitor` | Basic batch preparation status |
| `pipeline` | Comprehensive pipeline monitoring |
| `status` | Service status overview |
| `listener-logs` | P2P listener activity |
| `dequeuer-logs` | Submission processing logs |
| `finalizer-logs` | Batch finalization logs |
| `event-monitor-logs` | Epoch release events |
| `redis-logs` | Redis operation logs |

### Direct Script Usage

```bash
# Run comprehensive pipeline monitor
./scripts/monitor_pipeline.sh

# Specify custom container
./scripts/monitor_pipeline.sh container-name
```

## Redis Keys Structure

### Submission Tracking (Updated Format)
```
submissionQueue                                     # Raw P2P submissions
{protocol}:{market}:processed:{sequencerID}:{id}    # Individual processed submissions (10min TTL)
{protocol}:{market}:epoch:{epochId}:processed       # Set of submission IDs per epoch (1hr TTL)
{protocol}:{market}:epoch:{epochId}:project:{pid}:votes # Vote counts per CID
```

### Window Management (Updated Format)
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
metrics:total_processed                   # Total submissions processed
metrics:processing_rate                   # Submissions per minute
metrics:avg_latency                       # Average processing time
```

## Worker Status Values

| Status | Description |
|--------|-------------|
| `idle` | Worker available for tasks |
| `processing` | Actively processing batch |
| `failed` | Error state requiring attention |

## Health Indicators

### Healthy Pipeline
- ✅ Queue depth < 100
- ✅ Finalization queue < 10 batches
- ✅ Worker heartbeats < 60s old
- ✅ Aggregation queue < 5 epochs

### Warning Signs
- ⚠️ Queue depth > 100 (backlog forming)
- ⚠️ Worker heartbeat > 60s (stale worker)
- ⚠️ Finalization queue > 10 (workers overwhelmed)

### Critical Issues
- 🔴 Queue depth > 1000 (severe backlog)
- 🔴 No worker heartbeats (workers down)
- 🔴 Aggregation blocked (parts incomplete)

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
./launch.sh event-monitor-logs

# Verify EVENT_START_BLOCK setting
grep EVENT_START_BLOCK .env
```

### Workers Not Visible
Workers only appear when implemented. Current status:
- ✅ Dequeuer workers (5 instances)
- ⏳ Finalizer workers (TODO)
- ⏳ Aggregation worker (TODO)

### Queue Backlog
```bash
# Check dequeuer performance
./launch.sh dequeuer-logs | grep "Processing submission"

# Monitor queue depth trends
watch -n 5 "./launch.sh monitor | grep 'Pending'"
```

### Data Format Issues
The system now handles both batch and single submission formats with advanced conversion strategies:
```bash
# Check for batch format submissions
./launch.sh dequeuer-logs | grep "P2PSnapshotSubmission"

# Check field name changes (snake_case → camelCase)
./launch.sh dequeuer-logs | grep "epochId\|projectId"

# Verify conversion strategy
docker exec powerloom-sequencer-validator-dequeuer-1 \
  printenv SUBMISSION_FORMAT_STRATEGY

# Manual format override
# Possible values: 'auto', 'single', 'batch'
SUBMISSION_FORMAT_STRATEGY=single ./launch.sh distributed
```

#### Conversion Troubleshooting
- **Auto Strategy**: Automatically detects and converts submission formats
- **Single Strategy**: Forces single submission parsing
- **Batch Strategy**: Forces batch submission parsing

##### Common Conversion Mappings
| Old (snake_case) | New (camelCase) |
|-----------------|----------------|
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

**Tip**: Always use the most recent version of launch scripts and monitor containers for the latest conversion utilities.

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

## Future Enhancements

Planned monitoring improvements:
- HTTP metrics endpoint (`/metrics`)
- Prometheus integration
- Grafana dashboards
- WebSocket real-time updates
- Historical trend analysis

## Related Documentation

- [VPS_DEPLOYMENT.md](../VPS_DEPLOYMENT.md) - Deployment and operations guide
- [PHASE_2_CURRENT_WORK.md](../../PHASE_2_CURRENT_WORK.md) - Architecture overview
- [PROJECT_STATUS.md](../../PROJECT_STATUS.md) - Implementation status