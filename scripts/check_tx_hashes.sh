#!/bin/bash
# Quick script to check transaction hash presence for epochs
#
# This script wraps analyze_epoch_status.py to provide a convenient way to check
# transaction hash presence across active epochs.
#
# Usage:
#   ./scripts/check_tx_hashes.sh [api_url] [active_protocol] [active_market] [status_protocol] [status_market]
#
# Example:
#   ./scripts/check_tx_hashes.sh \
#     http://localhost:9092 \
#     0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401 \
#     0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d \
#     0xC9e7304f719D35919b0371d8B242ab59E0966d63 \
#     0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API_URL="${1:-http://localhost:9092}"
ACTIVE_PROTOCOL="${2:-0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401}"
ACTIVE_MARKET="${3:-0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d}"
STATUS_PROTOCOL="${4:-0xC9e7304f719D35919b0371d8B242ab59E0966d63}"
STATUS_MARKET="${5:-0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f}"

echo "=== Transaction Hash Analysis ==="
echo "Active epochs protocol: $ACTIVE_PROTOCOL"
echo "Status check protocol: $STATUS_PROTOCOL"
echo ""

python3 "${SCRIPT_DIR}/analyze_epoch_status.py" \
  --api-url "$API_URL" \
  --active-protocol "$ACTIVE_PROTOCOL" \
  --active-market "$ACTIVE_MARKET" \
  --status-protocol "$STATUS_PROTOCOL" \
  --status-market "$STATUS_MARKET" \
  --output summary

echo ""
echo "=== Detailed Table ==="
python3 "${SCRIPT_DIR}/analyze_epoch_status.py" \
  --api-url "$API_URL" \
  --active-protocol "$ACTIVE_PROTOCOL" \
  --active-market "$ACTIVE_MARKET" \
  --status-protocol "$STATUS_PROTOCOL" \
  --status-market "$STATUS_MARKET" \
  --output table

