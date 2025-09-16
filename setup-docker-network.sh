#!/bin/bash

# Setup script for creating shared Docker network between sequencer and IPFS containers

echo "Setting up dsv-internal-network for cross-container communication..."

# Create the shared network
docker network create --driver bridge dsv-internal-network 2>/dev/null || {
    echo "Network 'dsv-internal-network' already exists"
}

echo "✓ Network created/verified"

# Connect IPFS container to the shared network
echo "Connecting IPFS container to shared network..."
docker network connect dsv-internal-network root-ipfs-1 2>/dev/null || {
    echo "IPFS container already connected or error occurred"
}

echo "✓ IPFS container connected"

# Get IPFS container's IP in the shared network
# Note: Docker converts hyphens to underscores in network names for inspect
IPFS_IP=$(docker inspect root-ipfs-1 -f '{{.NetworkSettings.Networks.dsv_internal_network.IPAddress}}' 2>/dev/null || echo "")

echo ""
echo "========================================="
echo "Network setup complete!"
echo "========================================="
echo ""
echo "IPFS container is now accessible from sequencer containers"
echo ""
echo "Update your .env file with ONE of these options:"
echo ""
echo "Option 1 - Use container name (recommended):"
echo "  IPFS_HOST=/dns/root-ipfs-1/tcp/5001"
echo ""
if [ ! -z "$IPFS_IP" ]; then
    echo "Option 2 - Use container IP in shared network:"
    echo "  IPFS_HOST=/ip4/$IPFS_IP/tcp/5001"
    echo ""
fi
echo "Then restart your sequencer containers:"
echo "  docker-compose down && docker-compose up -d"
echo ""