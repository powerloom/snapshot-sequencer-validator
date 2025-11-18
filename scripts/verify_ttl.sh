#!/bin/bash

# TTL Verification Script for DSV Monitoring
# Checks that all metrics keys have proper TTLs set

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Redis connection
REDIS_HOST="${REDIS_HOST:-localhost}"
REDIS_PORT="${REDIS_PORT:-6379}"
REDIS_CMD="redis-cli -h $REDIS_HOST -p $REDIS_PORT"

echo "========================================="
echo "DSV Monitoring TTL Verification"
echo "Redis: $REDIS_HOST:$REDIS_PORT"
echo "========================================="
echo ""

# Track issues
NO_TTL_KEYS=()
EXPIRED_SOON_KEYS=()
SORTED_SETS=()
PASS_COUNT=0
FAIL_COUNT=0

# Function to check TTL
check_ttl() {
    local pattern=$1
    local expected_ttl=$2
    local description=$3

    echo "Checking: $description"
    echo "Pattern: $pattern"

    # Use SCAN to find keys
    cursor=0
    while [ "$cursor" != "0" ] || [ -z "$processed" ]; do
        processed=1
        result=$($REDIS_CMD --scan --pattern "$pattern" --count 100)
        cursor=$(echo "$result" | head -1)
        keys=$(echo "$result" | tail -n +2)

        if [ -n "$keys" ]; then
            while IFS= read -r key; do
                if [ -n "$key" ]; then
                    # Get key type
                    key_type=$($REDIS_CMD TYPE "$key" 2>/dev/null | tr -d '\r')

                    # Get TTL
                    ttl=$($REDIS_CMD TTL "$key" 2>/dev/null | tr -d '\r')

                    if [ "$key_type" = "zset" ]; then
                        # Sorted sets should not have TTL (pruned by worker)
                        if [ "$ttl" = "-1" ]; then
                            echo -e "  ${GREEN}✓${NC} $key (sorted set, no TTL - pruned by worker)"
                            SORTED_SETS+=("$key")
                            PASS_COUNT=$((PASS_COUNT + 1))
                        else
                            echo -e "  ${RED}✗${NC} $key (sorted set has TTL: $ttl seconds)"
                            FAIL_COUNT=$((FAIL_COUNT + 1))
                        fi
                    else
                        # Other types should have TTL
                        if [ "$ttl" = "-1" ]; then
                            echo -e "  ${RED}✗${NC} $key (NO TTL SET)"
                            NO_TTL_KEYS+=("$key")
                            FAIL_COUNT=$((FAIL_COUNT + 1))
                        elif [ "$ttl" = "-2" ]; then
                            # Key doesn't exist
                            continue
                        elif [ "$ttl" -lt 60 ] && [ "$ttl" -gt 0 ]; then
                            echo -e "  ${YELLOW}⚠${NC} $key (expires soon: ${ttl}s)"
                            EXPIRED_SOON_KEYS+=("$key")
                            PASS_COUNT=$((PASS_COUNT + 1))
                        else
                            echo -e "  ${GREEN}✓${NC} $key (TTL: ${ttl}s)"
                            PASS_COUNT=$((PASS_COUNT + 1))
                        fi
                    fi
                fi
            done <<< "$keys"
        fi

        if [ "$cursor" = "0" ]; then
            break
        fi
    done
    echo ""
}

# Check all monitoring key patterns
echo "1. SUBMISSION METRICS"
echo "====================="
check_ttl "metrics:submission:*" 3600 "Submission details (1hr TTL expected)"
check_ttl "metrics:submissions:timeline" 0 "Submissions timeline (sorted set, no TTL)"
check_ttl "metrics:hourly:*:submissions" 7200 "Hourly submission counters (2hr TTL)"
echo ""

echo "2. VALIDATION METRICS"
echo "====================="
check_ttl "metrics:validation:*" 3600 "Validation details (1hr TTL expected)"
check_ttl "metrics:validations:timeline" 0 "Validations timeline (sorted set, no TTL)"
check_ttl "metrics:epoch:*:validated" 7200 "Epoch validation sets (2hr TTL)"
check_ttl "metrics:hourly:*:validations" 7200 "Hourly validation counters (2hr TTL)"
echo ""

echo "3. EPOCH METRICS"
echo "================"
check_ttl "metrics:epoch:*:info" 7200 "Epoch info (2hr TTL expected)"
check_ttl "metrics:epoch:*:parts" 7200 "Epoch parts counter (2hr TTL)"
check_ttl "metrics:epochs:timeline" 0 "Epochs timeline (sorted set, no TTL)"
echo ""

echo "4. BATCH METRICS"
echo "================"
check_ttl "metrics:batch:local:*" 86400 "Local batch data (24hr TTL)"
check_ttl "metrics:batch:aggregated:*" 86400 "Aggregated batch data (24hr TTL)"
check_ttl "metrics:batch:*:validators" 86400 "Validator lists (24hr TTL)"
check_ttl "metrics:batches:timeline" 0 "Batches timeline (sorted set, no TTL)"
echo ""

echo "5. PART METRICS"
echo "==============="
check_ttl "metrics:part:*" 3600 "Part details (1hr TTL expected)"
check_ttl "metrics:parts:timeline" 0 "Parts timeline (sorted set, no TTL)"
echo ""

echo "6. VALIDATOR METRICS"
echo "===================="
check_ttl "metrics:validator:*:batches" 0 "Validator batches (sorted set, no TTL)"
echo ""

echo "7. AGGREGATED STATS"
echo "==================="
check_ttl "dashboard:summary" 60 "Dashboard summary (60s TTL)"
check_ttl "stats:current" 60 "Current stats (60s TTL)"
check_ttl "stats:hourly:*" 7200 "Hourly stats (2hr TTL)"
check_ttl "stats:daily" 86400 "Daily stats (24hr TTL)"
check_ttl "timeline:*" 0 "Timeline sorted sets (no TTL, pruned)"
echo ""

# Summary
echo "========================================="
echo "VERIFICATION SUMMARY"
echo "========================================="
echo -e "${GREEN}Passed:${NC} $PASS_COUNT"
echo -e "${RED}Failed:${NC} $FAIL_COUNT"
echo ""

if [ ${#NO_TTL_KEYS[@]} -gt 0 ]; then
    echo -e "${RED}Keys without TTL (should have TTL):${NC}"
    for key in "${NO_TTL_KEYS[@]}"; do
        echo "  - $key"
    done
    echo ""
fi

if [ ${#EXPIRED_SOON_KEYS[@]} -gt 0 ]; then
    echo -e "${YELLOW}Keys expiring soon (<60s):${NC}"
    for key in "${EXPIRED_SOON_KEYS[@]}"; do
        echo "  - $key"
    done
    echo ""
fi

if [ ${#SORTED_SETS[@]} -gt 0 ]; then
    echo -e "${GREEN}Sorted sets (correctly have no TTL, pruned by worker):${NC}"
    for key in "${SORTED_SETS[@]}"; do
        echo "  - $key"
    done
    echo ""
fi

# Check sorted set sizes (should be pruned to 24hr)
echo "========================================="
echo "SORTED SET SIZE CHECK (24hr pruning)"
echo "========================================="

check_zset_size() {
    local key=$1
    local max_expected=$2

    size=$($REDIS_CMD ZCARD "$key" 2>/dev/null | tr -d '\r')
    if [ -n "$size" ] && [ "$size" != "0" ]; then
        if [ "$size" -gt "$max_expected" ]; then
            echo -e "  ${RED}✗${NC} $key: $size entries (>$max_expected, may need pruning)"
        else
            echo -e "  ${GREEN}✓${NC} $key: $size entries"
        fi
    fi
}

# Check timeline sorted sets (should have ~24hr of data at most)
# Assuming 1 entry per second worst case = 86400 entries max
echo "Checking sorted set sizes..."
check_zset_size "metrics:submissions:timeline" 100000
check_zset_size "metrics:validations:timeline" 100000
check_zset_size "metrics:epochs:timeline" 1000
check_zset_size "metrics:batches:timeline" 1000
check_zset_size "metrics:parts:timeline" 10000

echo ""
echo "========================================="

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✅ All TTL checks passed!${NC}"
    exit 0
else
    echo -e "${RED}❌ Some TTL checks failed. Review keys above.${NC}"
    exit 1
fi