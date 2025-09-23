#!/bin/bash

# FORCE COMPLETE REBUILD - No cached layers whatsoever

echo "Stopping all containers..."
./dsv.sh stop

echo "Removing ALL related Docker images..."
docker images | grep -E 'snapshot-sequencer|sequencer-validator' | awk '{print $3}' | xargs -r docker rmi -f 2>/dev/null || true

echo "Pruning Docker builder cache completely..."
docker builder prune -af --verbose

echo "Removing all build cache..."
docker buildx prune -af 2>/dev/null || true

echo "Pruning system..."
docker system prune -af --volumes

echo "Verifying no images remain..."
docker images | grep -E 'snapshot|sequencer|validator' && echo "WARNING: Some images still exist!" || echo "All images removed successfully"

echo "Building with absolutely no cache..."
# Use timestamp to force cache invalidation
export CACHEBUST=$(date +%s)
DOCKER_BUILDKIT=0 docker compose -f docker-compose.separated.yml build --no-cache --pull --build-arg CACHEBUST=$CACHEBUST

echo "Starting services..."
./dsv.sh start

echo "Build complete! Check logs with: ./dsv.sh logs"   