# DSV vs Centralized Sequencer Comparison Script

This script verifies that the DSV (Decentralized Sequencer Validator) network produces equivalent results to the centralized sequencer before migration.

## Purpose

The script performs two critical comparisons:

1. **Submission Verification**: Ensures all submissions from the full node (`snapshotter-core-edge`) appear in Level 2 aggregated batches from DSV nodes
2. **On-Chain Commitment Verification**: Ensures on-chain commitments match between legacy and new protocol state contracts

## Prerequisites

```bash
pip install redis web3 eth-utils
```

## Usage

```bash
python scripts/compare_dsv_vs_centralized.py \
    --epoch-id <epoch_id> \
    --protocol <new_protocol_state_address> \
    --market <new_data_market_address> \
    --legacy-protocol <legacy_protocol_state_address> \
    --legacy-market <legacy_data_market_address> \
    --rpc-url <ethereum_rpc_url> \
    [--redis-host localhost] \
    [--redis-port 6379]
```

### Example

```bash
python scripts/compare_dsv_vs_centralized.py \
    --epoch-id 23875280 \
    --protocol 0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401 \
    --market 0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d \
    --legacy-protocol 0xE88E5f64AEB483d7057645326AdDFA24A3B312DF \
    --legacy-market 0x0C2E22fe7526fAeF28E7A58c84f8723dEFcE200c \
    --rpc-url https://rpc-devnet.powerloom.dev
```

## What It Checks

### 1. Submission Verification

- Extracts all submissions from Redis for the specified epoch
- Identifies full node submissions (from `snapshotter-core-edge`)
- Retrieves Level 2 aggregated batch from DSV nodes
- Verifies that all full node submission CIDs appear in the Level 2 batch

**Redis Keys Used:**
- `{protocol}:{market}:epoch:{epochId}:submissions:ids` (ZSET)
- `{protocol}:{market}:epoch:{epochId}:submissions:data` (HASH)
- `{protocol}:{market}:batch:aggregated:{epochId}` (STRING)

### 2. On-Chain Commitment Verification

- Queries new protocol state contract for epoch commitments
- Queries legacy protocol state contract for epoch commitments
- Compares multiple dimensions:
  1. **Batch CIDs**: All batch CIDs submitted for the epoch
  2. **Projects per Batch**: Projects contained in each batch CID
  3. **Merkle Roots**: `finalizedCidsRootHash` (sequencer attestation) for each batch
  4. **Individual Project CIDs**: Snapshot CID for each project in the epoch

**Contract Mappings Used:**
- `epochIdToBatchCids[epochId]` → Array of batch CIDs for epoch
- `batchCidToProjects[batchCid]` → Array of project IDs in batch
- `batchCidSequencerAttestation[batchCid]` → Merkle root (finalizedCidsRootHash)
- `snapshotStatus[projectId][epochId].snapshotCid` → Individual project snapshot CID

**Contract Functions Used (via ProtocolState):**
- `epochIdToBatchCids(dataMarket, epochId)` → Returns all batch CIDs
- `batchCidToProjects(dataMarket, batchCid)` → Returns projects in batch
- `batchCidSequencerAttestation(dataMarket, batchCid)` → Returns merkle root
- `snapshotStatus(dataMarket, projectId, epochId)` → Returns (status, cid, timestamp)

**Events Checked:**
- `SnapshotBatchSubmitted` - Batch submitted within submission window
- `DelayedBatchSubmitted` - Batch submitted after submission window

## Output

The script outputs:

1. **Console Output**: Step-by-step progress and comparison results
2. **JSON Results**: Complete comparison data in JSON format

### Example Output

```
================================================================================
Comparing DSV vs Centralized Sequencer for Epoch 23875280
================================================================================

📥 Extracting submissions from Redis...
   Found 5 projects with submissions
   - 0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984: 3 submissions
   ...

🔍 Extracting full node submissions...
   Found 5 projects from full node
   Total full node CIDs: 15

📦 Extracting Level 2 aggregated batch...
   Level 2 batch found with 5 projects
   IPFS CID: Qm...
   Merkle Root: 0x...

🔬 Comparing full node submissions with Level 2 batch...
   ✅ All full node submissions present in Level 2 batch

⛓️  Comparing on-chain commitments...
   ✅ On-chain commitments match between legacy and new contracts

================================================================================
SUMMARY
================================================================================
Epoch ID: 23875280
Submission Verification: ✅ PASS
On-Chain Verification: ✅ PASS
================================================================================
```

## Troubleshooting

### No submissions found

- Verify Redis connection and that submissions exist for the epoch
- Check that protocol/market addresses match your DSV node configuration
- Ensure epoch ID is correct and submissions have been processed

### Level 2 batch not found

- Verify Level 2 aggregation has completed for the epoch
- Check aggregator logs for errors
- Ensure aggregation window has expired (default 30 seconds)

### On-chain queries fail

- Verify RPC URL is accessible
- Check contract addresses are correct (legacy vs new)
- Ensure ABI file exists at `abi/PowerloomProtocolState.abi.json`
- Verify epoch has been committed on-chain via `submitSubmissionBatch()`
- Check that both centralized sequencer (legacy) and DSV nodes (new) have submitted batches
- Verify contract functions are accessible (may need to check if contracts are deployed)

### Missing submissions in Level 2

- Check P2P network connectivity between full node and DSV nodes
- Verify submissions were received by DSV nodes (check dequeuer logs)
- Check Level 1 aggregation completed successfully
- Verify aggregation window timing

## Integration with Monitoring

This script can be integrated into monitoring pipelines to:

- Run periodically after each epoch finalization
- Alert on mismatches between DSV and centralized sequencer
- Generate reports for epoch-by-epoch comparison
- Validate migration readiness before switching traffic

## Related Documentation

- [AGGREGATOR_WORKFLOW.md](../docs/AGGREGATOR_WORKFLOW.md): Level 2 aggregation details
- [PROTOCOL_META_REFERENCE.md](../../ai-coord-docs/specs/PROTOCOL_META_REFERENCE.md): Complete protocol workflow
- [PROTOCOL_STATE_TRANSITIONS.md](../../ai-coord-docs/specs/PROTOCOL_STATE_TRANSITIONS.md): State machine documentation

