#!/bin/bash

# Script to show detailed batch metadata including IPFS CIDs
# Usage: ./batch_details.sh [epoch_id]

# Color definitions
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Try to find a running container
CONTAINER=$(docker ps --format "{{.Names}}" | grep -E "sequencer|listener|dequeuer|finalizer" | head -1)

if [ -z "$CONTAINER" ]; then
    echo -e "${RED}Error: No running sequencer containers found${NC}"
    echo "Start services first with: ./dsv.sh distributed or ./dsv.sh sequencer"
    exit 1
fi

EPOCH_ID=$1

echo -e "${CYAN}📦 Batch Metadata Viewer${NC}"
echo -e "${CYAN}══════════════════════════════${NC}"
echo ""

docker exec $CONTAINER /bin/sh -c "
    REDIS_HOST=\${REDIS_HOST:-redis}
    REDIS_PORT=\${REDIS_PORT:-6379}

    if [ -z '$EPOCH_ID' ]; then
        echo '🔍 Fetching recent batches...'
        echo ''

        # Get last 10 finalized batches
        BATCHES=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT KEYS 'batch:finalized:*' 2>/dev/null | sort -rn | head -10)

        if [ -z \"\$BATCHES\" ]; then
            echo '  No finalized batches found'
            exit 0
        fi

        for batch_key in \$BATCHES; do
            EPOCH=\$(echo \"\$batch_key\" | grep -oE '[0-9]+\$')
            echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"
            echo \"📊 Epoch \$EPOCH\"
            echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"

            # Get batch data as JSON
            BATCH_JSON=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"\$batch_key\" 2>/dev/null)

            if [ ! -z \"\$BATCH_JSON\" ]; then
                # Extract fields from JSON
                IPFS_CID=\$(echo \"\$BATCH_JSON\" | grep -o '\"batch_ipfs_cid\":\"[^\"]*\"' | cut -d'\"' -f4)
                MERKLE_ROOT=\$(echo \"\$BATCH_JSON\" | grep -o '\"merkle_root\":\"[^\"]*\"' | cut -d'\"' -f4)
                TIMESTAMP=\$(echo \"\$BATCH_JSON\" | grep -o '\"timestamp\":[0-9]*' | cut -d':' -f2)

                if [ ! -z \"\$IPFS_CID\" ]; then
                    echo \"  🌐 IPFS CID: \$IPFS_CID\"
                fi

                if [ ! -z \"\$MERKLE_ROOT\" ]; then
                    echo \"  🔐 Merkle Root: \${MERKLE_ROOT:0:20}...\"
                fi

                if [ ! -z \"\$TIMESTAMP\" ]; then
                    # Convert timestamp to readable format if possible
                    echo \"  ⏰ Timestamp: \$TIMESTAMP\"
                fi

                # Count project submissions
                PROJECT_COUNT=\$(echo \"\$BATCH_JSON\" | grep -o '\"project_id\":\"[^\"]*\"' | wc -l)
                echo \"  📁 Projects: \$PROJECT_COUNT\"

                # Show submission details
                echo \"\"
                echo \"  📝 Submission Details:\"
                # Extract submission metadata
                SUBMISSIONS=\$(echo \"\$BATCH_JSON\" | grep -o '\"submitter_id\":\"[^\"]*\"' | cut -d'\"' -f4 | sort | uniq -c)
                if [ ! -z \"\$SUBMISSIONS\" ]; then
                    echo \"\$SUBMISSIONS\" | while read count submitter; do
                        echo \"    • Submitter \$submitter: \$count submissions\"
                    done
                else
                    echo \"    No submission details available\"
                fi
            else
                echo \"  ⚠️  No detailed data available\"
            fi
            echo \"\"
        done

        echo \"💡 To view a specific batch: ./scripts/batch_details.sh <epoch_id>\"
    else
        # Show specific batch details
        BATCH_KEY=\"batch:finalized:\$EPOCH_ID\"
        echo \"🔍 Fetching batch for Epoch \$EPOCH_ID...\"
        echo \"\"

        BATCH_JSON=\$(redis-cli -h \$REDIS_HOST -p \$REDIS_PORT GET \"\$BATCH_KEY\" 2>/dev/null)

        if [ -z \"\$BATCH_JSON\" ]; then
            echo \"❌ Batch not found for epoch \$EPOCH_ID\"
            echo \"\"
            echo \"Available epochs:\"
            redis-cli -h \$REDIS_HOST -p \$REDIS_PORT KEYS 'batch:finalized:*' 2>/dev/null | sed 's/batch:finalized:/  • Epoch /' | sort -rn | head -20
            exit 1
        fi

        echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"
        echo \"📊 Detailed Batch for Epoch \$EPOCH_ID\"
        echo \"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\"

        # Extract and display all fields
        IPFS_CID=\$(echo \"\$BATCH_JSON\" | grep -o '\"batch_ipfs_cid\":\"[^\"]*\"' | cut -d'\"' -f4)
        MERKLE_ROOT=\$(echo \"\$BATCH_JSON\" | grep -o '\"merkle_root\":\"[^\"]*\"' | cut -d'\"' -f4)
        TIMESTAMP=\$(echo \"\$BATCH_JSON\" | grep -o '\"timestamp\":[0-9]*' | cut -d':' -f2)

        echo \"📌 Core Information:\"
        echo \"  • Epoch ID: \$EPOCH_ID\"
        [ ! -z \"\$IPFS_CID\" ] && echo \"  • IPFS CID: \$IPFS_CID\"
        [ ! -z \"\$MERKLE_ROOT\" ] && echo \"  • Merkle Root: \$MERKLE_ROOT\"
        [ ! -z \"\$TIMESTAMP\" ] && echo \"  • Timestamp: \$TIMESTAMP\"

        echo \"\"
        echo \"📁 Project Submissions:\"
        # Parse project votes
        echo \"\$BATCH_JSON\" | grep -o '\"[^\"]*\":{\"[^\"]*\":[0-9]*' | while read -r line; do
            PROJECT=\$(echo \"\$line\" | cut -d'\"' -f2)
            CID_VOTES=\$(echo \"\$line\" | sed 's/.*{//')
            echo \"  • Project: \$PROJECT\"
            echo \"    Consensus: \$CID_VOTES\"
        done

        echo \"\"
        echo \"👥 Submitters:\"
        SUBMITTERS=\$(echo \"\$BATCH_JSON\" | grep -o '\"submitter_id\":\"[^\"]*\"' | cut -d'\"' -f4 | sort | uniq -c)
        if [ ! -z \"\$SUBMITTERS\" ]; then
            echo \"\$SUBMITTERS\" | while read count submitter; do
                echo \"  • \$submitter: \$count submissions\"
            done
        else
            echo \"  No submitter details available\"
        fi

        echo \"\"
        echo \"📄 Raw JSON (truncated):\"
        echo \"\$BATCH_JSON\" | head -c 500
        echo \"...\"
    fi
"