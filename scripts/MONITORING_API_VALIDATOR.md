# Monitoring API Validator

A comprehensive test suite for validating monitoring API endpoints against Redis data and protocol state contracts. This tool helps developers, contributors, and operators quickly verify the consistency and accuracy of monitoring API responses.

## Features

- **Multi-Layer Validation**: Validates API responses against Redis data and protocol state contracts
- **Environment File Support**: Uses existing `.env` configuration files for easy setup
- **Flexible Test Modes**: Quick validation, full testing, or endpoint-specific testing
- **Detailed Reporting**: Console and JSON output with comprehensive analysis
- **Redis Consistency Checks**: Verifies API responses match underlying Redis data
- **Contract Validation**: Validates on-chain data consistency (optional)
- **Performance Monitoring**: Tracks API response times and identifies bottlenecks

## Quick Start

### Using Environment Configuration Files

```bash
# Quick validation with validator2 config
./scripts/monitoring-api-validator.go -config localenvs/env.validator2.devnet -mode quick

# Full validation with deep analysis
./scripts/monitoring-api-validator.go -config localenvs/env.hznr.devnet.validator1 -mode full -depth deep

# Generate JSON report
./scripts/monitoring-api-validator.go -config localenvs/env.validator2.devnet -output json -output-file validation-report.json
```

### Using Command Line Parameters

```bash
# Direct parameter specification
./scripts/monitoring-api-validator.go \
  -api-url http://localhost:9001 \
  -redis-host localhost \
  -redis-port 6380 \
  -token "your-api-token" \
  -data-market "0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d" \
  -mode full
```

## Test Modes

### Quick Mode (`-mode quick`)
- Tests 3 critical endpoints:
  - Total Submissions
  - Eligible Nodes Count
  - Batch Count
- Basic validation (no Redis/contract checks)
- Fast execution (~5 seconds)

### Full Mode (`-mode full`)
- Tests all available endpoints
- Full Redis consistency checks
- Contract validation (if enabled)
- Comprehensive analysis (~30 seconds)

### Endpoints Mode (`-mode endpoints`)
- All endpoints with basic validation
- No deep Redis/contract analysis
- Balanced speed and coverage (~15 seconds)

## Validation Depth

### Basic (`-depth basic`)
- API response structure validation
- HTTP status code checks
- Response time monitoring
- Fast execution

### Deep (`-depth deep`)
- All basic checks plus:
- Redis key-value verification
- Data consistency analysis
- Contract state validation
- Detailed mismatch reporting

## Supported Endpoints

| Endpoint | Redis Validation | Contract Validation | Description |
|----------|------------------|-------------------|-------------|
| Total Submissions | ✅ | ❌ | Validates submission counts against Redis |
| Eligible Nodes Count | ✅ | ✅ | Checks eligible nodes vs Redis and contracts |
| Batch Count | ✅ | ✅ | Validates batch counts with Redis |
| Epoch Submission Details | ❌ | ❌ | Structure validation only |
| Active Nodes Count By Epoch | ✅ | ❌ | Active nodes vs Redis |
| Last Snapshot Submission | ✅ | ❌ | Timestamp validation with Redis |

## Configuration

### Required Parameters
- `API_AUTH_TOKEN`: Authentication token for API access
- `DATA_MARKET_ADDRESSES`: Target data market contract address

### Optional Parameters
- `API_BASE_URL`: Monitoring API endpoint (default: http://localhost:9001)
- `REDIS_HOST`: Redis server host (default: localhost)
- `REDIS_BIND_PORT`: Redis server port (default: 6379)
- `REDIS_DB`: Redis database number (default: 0)
- `PROTOCOL_STATE_CONTRACT`: Protocol state contract address
- `POWERLOOM_RPC_NODES`: RPC endpoint for contract validation

## Output Formats

### Console Output
```
=== Monitoring API Validation Report ===
Test Duration: 2.145s
Total Tests: 6
Passed: 5 | Failed: 1
API Response Time Avg: 245ms
Redis Consistency: 83.3%
Contract Consistency: 100.0%

=== Endpoint Results ===

Total Submissions:
  ✅ PASS (HTTP 200, 125ms)
  ✅ PASS (HTTP 200, 98ms) - redis validation passed

Eligible Nodes Count:
  ❌ FAIL (HTTP 200, 156ms) - redis eligible nodes mismatch: api=42, redis=45
```

### JSON Output
```json
{
  "test_config": {
    "api_base_url": "http://localhost:9001",
    "test_mode": "full",
    "validation_depth": "deep"
  },
  "summary": {
    "total_tests": 6,
    "passed_tests": 5,
    "failed_tests": 1,
    "api_response_time_avg": 245,
    "redis_consistency": 0.833,
    "contract_consistency": 1.0,
    "critical_issues": 1,
    "warning_count": 2
  },
  "endpoint_results": {
    "Total Submissions": [
      {
        "endpoint": "Total Submissions",
        "http_status": 200,
        "success": true,
        "response_time_ms": 125,
        "redis_matches": true,
        "redis_validation": "redis validation passed"
      }
    ]
  }
}
```

## Validation Details

### Redis Validation Logic

The validator checks API responses against actual Redis data:

1. **Total Submissions**: Compares API counts with `slot_submission_count` keys
2. **Eligible Nodes**: Validates against `eligible_nodes` Redis sets
3. **Batch Count**: Checks `batch_count` keys for epochs
4. **Active Nodes**: Verifies `active_snapshotters` set sizes
5. **Last Snapshot**: Timestamp validation against `last_snapshot_submission` keys

### Contract Validation

1. **Address Validation**: Ensures contract addresses are valid Ethereum addresses
2. **Blockchain Connectivity**: Verifies RPC connection and sync status
3. **State Consistency**: Basic on-chain data validation (future enhancement)

## Use Cases

### For Developers
- **Pre-commit Validation**: Quick sanity checks before deploying changes
- **Integration Testing**: Verify API changes don't break existing functionality
- **Performance Testing**: Monitor API response times during development

```bash
# Developer workflow
git add .
./scripts/monitoring-api-validator.go -config localenvs/env.validator2.devnet -mode quick
git commit -m "Feature: API updates"
```

### For Contributors
- **PR Validation**: Validate changes before creating pull requests
- **Environment Testing**: Test across different configurations
- **Documentation**: Provide test results with contributions

```bash
# Contributor testing
./scripts/monitoring-api-validator.go -config localenvs/env.hznr.devnet.validator1 -mode full -output json -output-file pr-validation.json
```

### For Operators
- **Production Monitoring**: Regular health checks of monitoring APIs
- **Troubleshooting**: Identify data inconsistencies quickly
- **Performance Analysis**: Track API performance over time

```bash
# Operator monitoring
./scripts/monitoring-api-validator.go -config production.env -mode full -depth deep -output json -output-file daily-health-check.json
```

## Common Issues and Solutions

### Authentication Failures
```
❌ FAIL - HTTP 401 - Incorrect Token!
```
**Solution**: Verify `API_AUTH_TOKEN` matches the monitoring service configuration

### Redis Connection Issues
```
❌ FAIL - redis key not found: 0x123...:slot_submission_count:1:1234
```
**Solution**: Check Redis connection and ensure the service is running and populated

### Contract Validation Failures
```
❌ FAIL - cannot connect to blockchain: dial tcp: connection refused
```
**Solution**: Verify RPC endpoint accessibility or use `-skip-contracts` flag

### API Response Time Issues
```
⚠️  WARNING - Slow response detected: 2847ms
```
**Solution**: Check API service performance and resource utilization

## Advanced Usage

### Custom Test Scenarios
```bash
# Test specific slot ID and epoch
API_BASE_URL="http://localhost:9001" \
DATA_MARKET_ADDRESSES="0x123..." \
API_AUTH_TOKEN="secret" \
./scripts/monitoring-api-validator.go -mode full -depth deep
```

### Automated Testing Integration
```bash
# CI/CD pipeline integration
#!/bin/bash
./scripts/monitoring-api-validator.go \
  -config $ENV_FILE \
  -mode quick \
  -output json \
  -output-file validation-results.json

# Check exit code for CI pass/fail
if [ $? -eq 0 ]; then
  echo "✅ API validation passed"
else
  echo "❌ API validation failed"
  exit 1
fi
```

### Performance Monitoring
```bash
# Daily performance tracking
DATE=$(date +%Y%m%d)
./scripts/monitoring-api-validator.go \
  -config production.env \
  -mode full \
  -output json \
  -output-file "reports/performance-${DATE}.json"
```

## Development

### Adding New Endpoints

1. Add endpoint to `getTestEndpoints()` function
2. Implement Redis validation function if needed
3. Add contract validation if applicable
4. Update documentation

### Extending Validation Logic

1. Add new validation functions for specific data types
2. Enhance Redis key pattern matching
3. Add more sophisticated contract interactions
4. Improve error reporting and analysis

## Troubleshooting

### Debug Mode
Enable verbose logging by setting environment variable:
```bash
DEBUG=1 ./scripts/monitoring-api-validator.go -config localenvs/env.validator2.devnet
```

### Network Issues
- Check API endpoint accessibility: `curl -f http://localhost:9001/swagger/`
- Verify Redis connection: `redis-cli -h localhost -p 6380 ping`
- Test RPC connectivity: `curl -X POST -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' $RPC_URL`

### Data Inconsistency
If you find Redis/API mismatches:
1. Check service logs for data processing errors
2. Verify data freshness in Redis
3. Validate API response structure
4. Check for data race conditions in updates

## Contributing

When contributing to the validator:
1. Add appropriate test cases for new functionality
2. Update documentation for new validation features
3. Ensure backward compatibility with existing configurations
4. Add comprehensive error handling for edge cases

## License

This monitoring API validator is part of the decentralized sequencer project and follows the same licensing terms.