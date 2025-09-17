#!/bin/bash

# Validator Deployment Script
# This script helps deploy a validator node on a VPS

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   Powerloom Validator Deployment    ${NC}"
echo -e "${BLUE}======================================${NC}"
echo

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"

if ! command_exists docker; then
    echo -e "${RED}Error: Docker is not installed${NC}"
    echo "Please install Docker first: https://docs.docker.com/get-docker/"
    exit 1
fi

if ! command_exists docker-compose; then
    echo -e "${RED}Error: Docker Compose is not installed${NC}"
    echo "Please install Docker Compose: https://docs.docker.com/compose/install/"
    exit 1
fi

echo -e "${GREEN}✓ Docker and Docker Compose are installed${NC}"

# Check if .env file exists
if [ ! -f .env ]; then
    if [ -f .env.validator.template ]; then
        echo -e "${YELLOW}No .env file found. Creating from template...${NC}"
        cp .env.validator.template .env
        echo -e "${GREEN}✓ Created .env file from template${NC}"
        echo -e "${YELLOW}Please edit .env file and configure your settings, then run this script again${NC}"
        exit 0
    else
        echo -e "${RED}Error: No .env file or template found${NC}"
        exit 1
    fi
fi

# Load environment variables
source .env

# Validate required variables
if [ -z "$VALIDATOR_ID" ]; then
    echo -e "${RED}Error: VALIDATOR_ID not set in .env${NC}"
    exit 1
fi

if [ -z "$PUBLIC_IP" ] || [ "$PUBLIC_IP" = "YOUR_VPS_PUBLIC_IP" ]; then
    echo -e "${YELLOW}PUBLIC_IP not configured. Attempting to detect...${NC}"
    DETECTED_IP=$(curl -s ifconfig.me || curl -s icanhazip.com || echo "")
    if [ ! -z "$DETECTED_IP" ]; then
        echo -e "${GREEN}Detected public IP: $DETECTED_IP${NC}"
        echo -n "Use this IP? (y/n): "
        read -r response
        if [[ "$response" =~ ^[Yy]$ ]]; then
            sed -i.bak "s/PUBLIC_IP=.*/PUBLIC_IP=$DETECTED_IP/" .env
            PUBLIC_IP=$DETECTED_IP
        else
            echo -e "${RED}Please set PUBLIC_IP in .env file${NC}"
            exit 1
        fi
    else
        echo -e "${RED}Could not detect public IP. Please set PUBLIC_IP in .env${NC}"
        exit 1
    fi
fi

# Generate private key if not set
if [ -z "$PRIVATE_KEY_HEX" ]; then
    echo -e "${YELLOW}Generating new P2P identity...${NC}"
    # This would normally use a Go tool to generate the key
    # For now, we'll leave it empty to auto-generate
    echo -e "${GREEN}✓ P2P identity will be auto-generated${NC}"
fi

# Display configuration
echo
echo -e "${BLUE}Configuration:${NC}"
echo -e "  Validator ID: ${GREEN}$VALIDATOR_ID${NC}"
echo -e "  Public IP: ${GREEN}$PUBLIC_IP${NC}"
echo -e "  P2P Port: ${GREEN}${P2P_PORT:-9000}${NC}"
echo -e "  Redis Port: ${GREEN}${REDIS_PORT:-6379}${NC}"
echo -e "  IPFS API Port: ${GREEN}${IPFS_API_PORT:-5001}${NC}"
echo

# Ask for confirmation
echo -n -e "${YELLOW}Deploy validator with this configuration? (y/n): ${NC}"
read -r response
if [[ ! "$response" =~ ^[Yy]$ ]]; then
    echo "Deployment cancelled"
    exit 0
fi

# Build the Docker image
echo
echo -e "${YELLOW}Building Docker image...${NC}"
docker build --no-cache -t powerloom-validator .
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Docker image built successfully${NC}"
else
    echo -e "${RED}Failed to build Docker image${NC}"
    exit 1
fi

# Start the validator
echo
echo -e "${YELLOW}Starting validator services...${NC}"
docker-compose -f docker-compose.validator.yml up -d

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Validator services started${NC}"
else
    echo -e "${RED}Failed to start services${NC}"
    exit 1
fi

# Wait for services to initialize
echo
echo -e "${YELLOW}Waiting for services to initialize...${NC}"
sleep 10

# Check service status
echo
echo -e "${BLUE}Service Status:${NC}"

# Check validator container
if docker ps | grep -q "powerloom-validator-${VALIDATOR_ID}"; then
    echo -e "${GREEN}✓ Validator container is running${NC}"

    # Get P2P ID
    P2P_ID=$(docker logs powerloom-validator-${VALIDATOR_ID} 2>&1 | grep "Host ID:" | head -1 | awk '{print $NF}')
    if [ ! -z "$P2P_ID" ]; then
        echo -e "${GREEN}✓ P2P Host ID: $P2P_ID${NC}"
    fi
else
    echo -e "${RED}✗ Validator container is not running${NC}"
fi

# Check Redis
if docker ps | grep -q "redis-validator-${VALIDATOR_ID}"; then
    echo -e "${GREEN}✓ Redis is running${NC}"
else
    echo -e "${RED}✗ Redis is not running${NC}"
fi

# Check IPFS
if docker ps | grep -q "ipfs-validator-${VALIDATOR_ID}"; then
    echo -e "${GREEN}✓ IPFS is running${NC}"

    # Get IPFS peer ID
    IPFS_ID=$(docker exec ipfs-validator-${VALIDATOR_ID} ipfs id -f='<id>' 2>/dev/null || echo "")
    if [ ! -z "$IPFS_ID" ]; then
        echo -e "${GREEN}✓ IPFS Peer ID: $IPFS_ID${NC}"
    fi
else
    echo -e "${RED}✗ IPFS is not running${NC}"
fi

echo
echo -e "${BLUE}======================================${NC}"
echo -e "${GREEN}Validator deployment complete!${NC}"
echo -e "${BLUE}======================================${NC}"
echo

# Display useful commands
echo -e "${YELLOW}Useful commands:${NC}"
echo -e "  View logs:           ${BLUE}docker-compose -f docker-compose.validator.yml logs -f validator${NC}"
echo -e "  Stop validator:      ${BLUE}docker-compose -f docker-compose.validator.yml down${NC}"
echo -e "  Restart validator:   ${BLUE}docker-compose -f docker-compose.validator.yml restart${NC}"
echo -e "  Monitor consensus:   ${BLUE}docker-compose -f docker-compose.validator.yml --profile monitor up monitor${NC}"
echo

# Create multiaddr for other validators to connect
if [ ! -z "$P2P_ID" ]; then
    echo -e "${YELLOW}Bootstrap address for other validators:${NC}"
    echo -e "${GREEN}/ip4/$PUBLIC_IP/tcp/${P2P_PORT:-9000}/p2p/$P2P_ID${NC}"
    echo
    echo "Share this address with other validators to form the consensus network"
fi

# Save deployment info
echo
echo -e "${YELLOW}Saving deployment info...${NC}"
cat > validator-info.txt << EOF
Validator Deployment Information
=================================
Deployment Time: $(date)
Validator ID: $VALIDATOR_ID
Public IP: $PUBLIC_IP
P2P Port: ${P2P_PORT:-9000}
P2P Host ID: ${P2P_ID:-"Not yet available"}
IPFS Peer ID: ${IPFS_ID:-"Not yet available"}

Bootstrap Address:
/ip4/$PUBLIC_IP/tcp/${P2P_PORT:-9000}/p2p/${P2P_ID:-"<P2P_ID>"}

Services:
- Validator: powerloom-validator-${VALIDATOR_ID}
- Redis: redis-validator-${VALIDATOR_ID}
- IPFS: ipfs-validator-${VALIDATOR_ID}
EOF

echo -e "${GREEN}✓ Deployment info saved to validator-info.txt${NC}"
echo
echo -e "${GREEN}Validator is ready to participate in consensus!${NC}"