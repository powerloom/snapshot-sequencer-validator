# IPFS Deployment Guide

This guide covers the proper deployment of IPFS nodes for the Decentralized Sequencer Validator (DSV) infrastructure.

## Container Architecture

The IPFS deployment uses a proper containerization approach with two services:

### 1. IPFS Node (`ipfs` service)
- **Image**: `ipfs/kubo:master-latest` (official Kubo image)
- **Purpose**: Main IPFS daemon for content storage and retrieval
- **Ports**:
  - API: 5001 (required for DSV components)
  - Swarm: 4001 (P2P communication)
  - Gateway: 8080 (internal access)

### 2. IPFS Cleanup Service (`ipfs-cleanup` service)
- **Image**: `ipfs/kubo:master-latest`
- **Purpose**: Sidecar container for periodic cleanup of old pins
- **Dependency**: Waits for IPFS node to be healthy before starting

## System Requirements

### Host System Configuration

For optimal IPFS performance, configure the host system with these settings:

```bash
# Add to /etc/sysctl.conf
# Increase TCP buffer sizes
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.tcp_rmem=4096 87380 16777216
net.ipv4.tcp_wmem=4096 87380 16777216

# Increase UDP buffer sizes
net.ipv4.udp_mem=16777216 16777216 16777216

# Increase the maximum backlog
net.core.netdev_max_backlog=10000

# Increase the maximum number of open files
fs.file-max=1000000

# Increase the maximum number of memory map areas
vm.max_map_count=262144

# Increase TCP connection handling
net.ipv4.tcp_max_syn_backlog=8192
net.ipv4.tcp_max_tw_buckets=2000000
```

Apply the settings:
```bash
sudo sysctl -p
sudo systemctl restart docker
```

### Resource Limits

The IPFS container includes these ulimits for optimal performance:

```yaml
ulimits:
  nofile:
    soft: 1000000  # High file descriptor limit for network connections
    hard: 1000000
  memlock:
    soft: -1       # Unlimited memory lock
    hard: -1
  nproc:
    soft: 32768    # High process limit for concurrent operations
    hard: 32768
```

## Environment Variables

### Required for IPFS Node
- `IPFS_PROFILE=server` - Predefined server configuration
- `IPFS_PATH=/data/ipfs` - IPFS data directory

### Optional Configuration
- `IPFS_CLEANUP_MAX_AGE_DAYS=7` - Days after which pins are eligible for cleanup
- `IPFS_CLEANUP_INTERVAL_HOURS=72` - Cleanup service run interval
- `IPFS_DATA_DIR=/data/ipfs` - Host directory for IPFS data persistence

### Port Configuration
- `IPFS_API_PORT=5001` - IPFS API port (exposed to host)
- `IPFS_SWARM_PORT=4001` - IPFS Swarm port (exposed to host)
- `IPFS_GATEWAY_PORT=8080` - IPFS Gateway port (exposed to host)

## Data Persistence

### Bind Mount Configuration
```yaml
volumes:
  - ${IPFS_DATA_DIR:-/data/ipfs}:/data/ipfs
```

Ensure the host directory has proper permissions:
```bash
sudo mkdir -p /data/ipfs
sudo chown -R $USER:$USER /data/ipfs
chmod 755 /data/ipfs
```

## Deployment Commands

### Start IPFS Services
```bash
# Start both IPFS node and cleanup service
docker-compose --profile ipfs up -d

# Start only IPFS node (no cleanup)
docker-compose --profile ipfs up -d ipfs
```

### Monitor IPFS Status
```bash
# Check container status
docker-compose ps ipfs ipfs-cleanup

# View IPFS logs
docker-compose logs -f ipfs

# View cleanup logs
docker-compose logs -f ipfs-cleanup

# Check IPFS node info
docker exec dsv-ipfs ipfs id
```

### Manual Cleanup
```bash
# Run one-time cleanup
docker exec dsv-ipfs-cleanup /scripts/ipfs-cleanup.sh --once

# Check repository size
docker exec dsv-ipfs ipfs repo stat
```

## Network Configuration

### Firewall Settings
Ensure these ports are open on the host firewall:
- 4001/tcp (Swarm)
- 4001/udp (Swarm discovery)
- 5001/tcp (API)
- 8080/tcp (Gateway - if external access needed)

### Security Considerations
- API port (5001) should only be accessible to trusted containers
- Gateway port (8080) can be exposed externally if needed
- Swarm ports enable P2P network participation

## Troubleshooting

### Common Issues

1. **IPFS fails to start**
   ```bash
   # Check if data directory exists and has correct permissions
   ls -la /data/ipfs

   # Remove corrupted repository and reinitialize
   rm -rf /data/ipfs/*
   docker-compose down ipfs
   docker-compose up -d ipfs
   ```

2. **High memory usage**
   ```bash
   # Check memory usage
   docker stats dsv-ipfs

   # Adjust storage limits
   docker exec dsv-ipfs ipfs config Datastore.StorageMax '"100GB"'
   ```

3. **Network connectivity issues**
   ```bash
   # Check swarm peers
   docker exec dsv-ipfs ipfs swarm peers

   # Check connection manager status
   docker exec dsv-ipfs ipfs swarm addrs local
   ```

### Performance Monitoring

Monitor these metrics:
- Container resource usage: `docker stats dsv-ipfs`
- Repository size: `docker exec dsv-ipfs ipfs repo stat`
- Network connections: `docker exec dsv-ipfs ipfs swarm peers`
- Pin status: `docker exec dsv-ipfs ipfs pin ls`

## Maintenance

### Regular Tasks
1. Monitor disk usage: IPFS data can grow significantly
2. Check cleanup logs: Ensure old pins are being removed
3. Update IPFS version: Pull latest `ipfs/kubo:master-latest`
4. Backup configuration: Export `/data/ipfs/config` if needed

### Cleanup Configuration
The cleanup service automatically:
- Removes pins older than `IPFS_CLEANUP_MAX_AGE_DAYS`
- Runs every `IPFS_CLEANUP_INTERVAL_HOURS`
- Preserves recent and important pins
- Performs garbage collection after unpinning

## Integration with DSV

### Connection Points
- Finalizer components connect to IPFS API on port 5001
- Aggregator stores and retrieves batches via IPFS
- Cleanup service maintains storage efficiency

### Expected Behavior
- DSV components automatically detect and use local IPFS
- Faster batch storage and retrieval vs remote IPFS
- Configurable storage limits prevent disk overflow
- Automatic cleanup maintains performance over time