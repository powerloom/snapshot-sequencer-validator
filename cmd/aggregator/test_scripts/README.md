# Event Log Check Test Scripts

These are standalone test scripts for verifying the `BatchSubmissionsCompleted` event log filtering logic.

**Note:** These files use `// +build ignore` build tags to prevent them from being compiled with the main package. They can be run independently.

## Usage

### Test specific epoch:
```bash
cd cmd/aggregator/test_scripts
go run test_event_check.go
```

### Test all recent epochs (last 10000 blocks):
```bash
cd cmd/aggregator/test_scripts
go run test_event_check_broad.go
```

## What they test

These scripts verify that the `checkEpochHasSubmission` method in `main.go` can correctly:
1. Load the ProtocolState ABI
2. Query `BatchSubmissionsCompleted` events from the ProtocolState contract
3. Filter by data market address and epoch ID
4. Return correct results indicating whether submissions have been completed

## Configuration

Update the following variables in the scripts to match your environment:
- `rpcURL`: RPC endpoint URL
- `protocolStateAddr`: ProtocolState contract address
- `dataMarketAddr`: Data market contract address
- `epochID`: Epoch ID to check (in `test_event_check.go`)

