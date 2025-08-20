#!/bin/bash

# Health check script for different service types

check_bootstrap() {
    # Check if bootstrap service is listening on P2P port
    nc -z localhost "${P2P_PORT:-9100}"
}

check_sequencer() {
    # Check if sequencer is listening on P2P port
    nc -z localhost "${P2P_PORT:-8001}" && \
    # Check if API port is available (if configured)
    if [ ! -z "$API_PORT" ]; then
        nc -z localhost "$API_PORT"
    else
        true
    fi
}

check_debugger() {
    # Check if debugger is listening on P2P port
    nc -z localhost "${P2P_PORT:-8001}"
}

# Main health check
case "$SERVICE_TYPE" in
    "bootstrap")
        check_bootstrap
        ;;
    "sequencer")
        check_sequencer
        ;;
    "debugger")
        check_debugger
        ;;
    *)
        echo "Unknown service type for health check: $SERVICE_TYPE"
        exit 1
        ;;
esac

exit_code=$?

if [ $exit_code -eq 0 ]; then
    echo "Health check passed for $SERVICE_TYPE service"
else
    echo "Health check failed for $SERVICE_TYPE service"
fi

exit $exit_code