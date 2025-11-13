# DSV Scripts Directory

This directory contains utility scripts for monitoring, validating, and managing Powerloom DSV nodes.

## 🚀 Monitoring API Validator

**New**: Comprehensive monitoring API validation tool

### Quick Start
```bash
# Quick validation with existing config
./scripts/validate-monitoring.sh localenvs/env.hznr.devnet.validator1

# Full validation with deep analysis
./scripts/validate-monitoring.sh localenvs/env.hznr.devnet.validator1 --full --deep

# Generate JSON report
./scripts/validate-monitoring.sh localenvs/env.hznr.devnet.validator1 --json --output-file validation.json
```

### Features
- ✅ API endpoint health checks
- ✅ Redis data consistency validation
- ✅ Performance monitoring (response times)
- ✅ JSON and console output formats
- ✅ Environment file support

**Documentation**: See [MONITORING_API_VALIDATOR.md](./MONITORING_API_VALIDATOR.md)

## 📋 Available Scripts

### Monitoring Scripts
- **`validate-monitoring.sh`** - Monitoring API validation wrapper
- **`monitor_api_client.sh`** - API monitoring client
- **`monitor_simple.sh`** - Simple monitoring script
- **`monitor_batch_docker.sh`** - Docker batch monitoring
- **`check_batch_status.sh`** - Batch status checker

### Validation Scripts
- **`validate-ipfs-deployment.sh`** - IPFS deployment validation
- **`monitoring-api-validator/`** - Go-based API validator tool

### Maintenance Scripts
- **`cleanup_stale_queue.sh`** - Queue cleanup utility
- **`verify_ttl.sh`** - TTL verification
- **`batch_details.sh`** - Batch information extractor

## 🔧 Usage

Most scripts use environment files for configuration. Use the existing `.env` files in your workspace or create new ones as needed.

```bash
# Example: Using environment config
./scripts/validate-monitoring.sh localenvs/env.validator2.devnet

# Example: Direct monitoring
./scripts/monitor_api_client.sh
```

## 📖 Documentation

- **Monitoring API Validator**: [MONITORING_API_VALIDATOR.md](./MONITORING_API_VALIDATOR.md)
- **DSV Node Setup**: [../docs/DSV_NODE_SETUP.md](../docs/DSV_NODE_SETUP.md)
- **Redis Keys**: [../REDIS_KEYS.md](../REDIS_KEYS.md)