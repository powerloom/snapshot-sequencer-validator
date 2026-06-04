# DSV Health Monitoring Scripts

Helper scripts for monitoring epoch processing health and detecting gaps.

## check_epoch_gaps.go

Queries both legacy and new ProtocolState contracts for `EpochReleased` events and compares against Redis to find:
- Gaps in epoch release sequence
- Epochs released but not processed

**Usage:**
```bash
go run scripts/check_epoch_gaps.go <rpc_url> [redis_host:port]
```

**Example:**
```bash
go run scripts/check_epoch_gaps.go https://devnet-orbit-rpc.aws2.powerloom.io/rpc?uuid=... localhost:6381
```

**Output:**
- Total epochs released from both contracts
- Epochs processed (found in Redis)
- Missing epochs in release sequence
- Unprocessed epochs (released but not in Redis)

## epoch_lifecycle_tracker.go

Tracks complete lifecycle of epochs from release to submission, showing all stages and timing.

**Usage:**
```bash
go run scripts/epoch_lifecycle_tracker.go <redis_host:port> <protocol> <market> [epoch_id]
```

**Example (single epoch):**
```bash
go run scripts/epoch_lifecycle_tracker.go localhost:6381 0xC9e7304f719D35919b0371d8B242ab59E0966d63 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f 23867390
```

**Example (recent epochs):**
```bash
go run scripts/epoch_lifecycle_tracker.go localhost:6381 0xC9e7304f719D35919b0371d8B242ab59E0966d63 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f
```

**Output:**
- Status of each stage: released, finalized, aggregated, vpa_priority, vpa_submission
- Timing between stages
- Missing steps identification
- Summary of complete/stuck/incomplete epochs

## Monitor API Endpoints

### GET /api/v1/vpa/timeline

Returns VPA timeline with epoch IDs sorted (descending - most recent first).

**Query Parameters:**
- `protocol` (optional): Protocol state identifier
- `market` (optional): Data market address
- `type` (optional): `priority`, `submission`, or `both` (default: `both`)
- `limit` (optional): Number of entries to return (default: 50)

### GET /api/v1/vpa/epoch/{epochID}/lifecycle

Tracks complete lifecycle of a specific epoch.

**Query Parameters:**
- `protocol` (optional): Protocol state identifier
- `market` (optional): Data market address

**Response:**
```json
{
  "epoch_id": "23867390",
  "stages": {
    "released": {
      "timestamp": 1234567890,
      "status": "completed",
      "details": {...}
    },
    "finalized": {
      "timestamp": 1234567910,
      "status": "completed",
      "details": {...}
    },
    "aggregated": {
      "timestamp": 1234567940,
      "status": "completed",
      "details": {...}
    },
    "vpa_priority": {
      "timestamp": 1234567950,
      "status": "assigned",
      "details": {...}
    },
    "vpa_submission": {
      "timestamp": 1234568000,
      "status": true,
      "details": {...}
    }
  },
  "timing": {
    "released_to_finalized_seconds": 20,
    "released_to_aggregated_seconds": 50,
    "released_to_submission_seconds": 110
  }
}
```

## Notes

- All API responses now sort epoch IDs in descending order (most recent first)
- Scripts use common sense approach: query contracts directly, compare with Redis state
- Lifecycle tracking shows exactly where epochs get stuck or miss steps

