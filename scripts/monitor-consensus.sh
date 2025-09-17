#!/bin/bash

# Monitor validator consensus status
# Usage: ./monitor-consensus.sh [redis-host] [redis-port]

REDIS_HOST=${1:-localhost}
REDIS_PORT=${2:-6379}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

clear
echo -e "${CYAN}==================================="
echo "    VALIDATOR CONSENSUS MONITOR    "
echo -e "===================================${NC}"
echo

while true; do
    # Get current epoch
    CURRENT_TIME=$(date +%s)
    CURRENT_EPOCH=$((CURRENT_TIME / 30))

    echo -e "${YELLOW}Current Epoch: ${CURRENT_EPOCH}${NC}"
    echo -e "${YELLOW}Time: $(date '+%Y-%m-%d %H:%M:%S')${NC}"
    echo

    # Check last 5 epochs
    echo -e "${BLUE}Recent Epoch Consensus Status:${NC}"
    echo "----------------------------------------"

    for i in {0..4}; do
        EPOCH=$((CURRENT_EPOCH - i))

        # Get consensus status
        STATUS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "consensus:epoch:${EPOCH}:status" 2>/dev/null)

        if [ ! -z "$STATUS" ] && [ "$STATUS" != "(nil)" ]; then
            # Parse JSON status
            CONSENSUS_REACHED=$(echo $STATUS | jq -r '.consensus_reached' 2>/dev/null)
            TOTAL_VALIDATORS=$(echo $STATUS | jq -r '.total_validators' 2>/dev/null)
            RECEIVED_BATCHES=$(echo $STATUS | jq -r '.received_batches' 2>/dev/null)
            CONSENSUS_CID=$(echo $STATUS | jq -r '.consensus_cid' 2>/dev/null)

            if [ "$CONSENSUS_REACHED" = "true" ]; then
                echo -e "Epoch ${EPOCH}: ${GREEN}✅ CONSENSUS REACHED${NC}"
                echo -e "  Validators: ${RECEIVED_BATCHES}/${TOTAL_VALIDATORS}"
                if [ ! -z "$CONSENSUS_CID" ] && [ "$CONSENSUS_CID" != "null" ]; then
                    echo -e "  CID: ${CONSENSUS_CID:0:20}..."
                fi
            else
                echo -e "Epoch ${EPOCH}: ${YELLOW}⏳ PENDING${NC}"
                echo -e "  Validators: ${RECEIVED_BATCHES}/${TOTAL_VALIDATORS}"
            fi
        else
            # Check if epoch has any validators
            VALIDATORS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT SMEMBERS "epoch:${EPOCH}:validators" 2>/dev/null | wc -l)
            if [ $VALIDATORS -gt 0 ]; then
                echo -e "Epoch ${EPOCH}: ${YELLOW}⏳ IN PROGRESS${NC}"
                echo -e "  Validators reporting: $VALIDATORS"
            else
                echo -e "Epoch ${EPOCH}: ${RED}No data${NC}"
            fi
        fi
    done

    echo
    echo -e "${BLUE}Validator Batches in Redis:${NC}"
    echo "----------------------------------------"

    # Count validator batches for current epoch
    VALIDATOR_KEYS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT --scan --pattern "validator:*:batch:${CURRENT_EPOCH}" 2>/dev/null | wc -l)
    echo -e "Current epoch batches: ${VALIDATOR_KEYS}"

    # List validators for current epoch
    VALIDATORS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT SMEMBERS "epoch:${CURRENT_EPOCH}:validators" 2>/dev/null)
    if [ ! -z "$VALIDATORS" ]; then
        echo -e "Active validators:"
        echo "$VALIDATORS" | while read -r validator; do
            if [ ! -z "$validator" ] && [ "$validator" != "(empty array)" ]; then
                echo -e "  • $validator"
            fi
        done
    fi

    echo
    echo -e "${BLUE}Consensus Results:${NC}"
    echo "----------------------------------------"

    # Check for recent consensus results
    for i in {0..2}; do
        EPOCH=$((CURRENT_EPOCH - i))
        RESULT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "consensus:epoch:${EPOCH}:result" 2>/dev/null)

        if [ ! -z "$RESULT" ] && [ "$RESULT" != "(nil)" ]; then
            CID=$(echo $RESULT | jq -r '.cid' 2>/dev/null)
            MERKLE=$(echo $RESULT | jq -r '.merkle_root' 2>/dev/null)
            TIMESTAMP=$(echo $RESULT | jq -r '.timestamp' 2>/dev/null)

            if [ ! -z "$CID" ] && [ "$CID" != "null" ]; then
                echo -e "Epoch ${EPOCH}: ${GREEN}✅${NC}"
                echo -e "  CID: ${CID:0:30}..."
                echo -e "  Merkle: ${MERKLE:0:16}..."

                # Convert timestamp to readable format
                if [ ! -z "$TIMESTAMP" ] && [ "$TIMESTAMP" != "null" ]; then
                    READABLE_TIME=$(date -d "@$TIMESTAMP" '+%H:%M:%S' 2>/dev/null || date -r "$TIMESTAMP" '+%H:%M:%S' 2>/dev/null)
                    echo -e "  Time: $READABLE_TIME"
                fi
            fi
        fi
    done

    echo
    echo -e "${MAGENTA}P2P Network Stats:${NC}"
    echo "----------------------------------------"

    # Check for any P2P-related keys
    P2P_PEERS=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT GET "p2p:connected_peers" 2>/dev/null)
    if [ ! -z "$P2P_PEERS" ] && [ "$P2P_PEERS" != "(nil)" ]; then
        echo -e "Connected peers: $P2P_PEERS"
    fi

    # Count total finalized batches
    FINALIZED_COUNT=$(redis-cli -h $REDIS_HOST -p $REDIS_PORT --scan --pattern "*:finalized:*" 2>/dev/null | wc -l)
    echo -e "Total finalized batches in Redis: $FINALIZED_COUNT"

    echo
    echo "----------------------------------------"
    echo -e "${CYAN}Refreshing in 10 seconds... (Ctrl+C to exit)${NC}"
    sleep 10
    clear
    echo -e "${CYAN}==================================="
    echo "    VALIDATOR CONSENSUS MONITOR    "
    echo -e "===================================${NC}"
    echo
done