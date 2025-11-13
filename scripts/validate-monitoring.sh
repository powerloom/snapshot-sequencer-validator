#!/bin/bash

# Monitoring API Validation Wrapper Script
# Makes it easy to run validation tests with common configurations

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALIDATOR_DIR="$SCRIPT_DIR/monitoring-api-validator"

# Default values
MODE="quick"
DEPTH="basic"
OUTPUT="console"
SKIP_CONTRACTS=false

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[VALIDATOR]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Function to show usage
show_usage() {
    cat << EOF
Monitoring API Validation Tool

Usage: $0 [OPTIONS] CONFIG_FILE

Examples:
    $0 localenvs/env.validator2.devnet                    # Quick validation
    $0 localenvs/env.hznr.devnet.validator1 --full       # Full validation
    $0 localenvs/env.validator2.devnet --deep --json     # Deep validation with JSON output
    $0 --help                                            # Show this help

Options:
    --quick, -q          Quick validation mode (default)
    --full, -f           Full validation mode
    --endpoints, -e      Endpoints validation mode
    --deep, -d           Deep validation with Redis/contract checks
    --basic, -b          Basic validation only (default)
    --json, -j           Output in JSON format
    --console, -c        Output to console (default)
    --skip-contracts     Skip contract validation
    --output-file FILE   Save JSON output to file
    --help, -h           Show this help message

CONFIG_FILE: Path to environment configuration file

EOF
}

# Parse command line arguments
CONFIG_FILE=""
OUTPUT_FILE=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --quick|-q)
            MODE="quick"
            shift
            ;;
        --full|-f)
            MODE="full"
            shift
            ;;
        --endpoints|-e)
            MODE="endpoints"
            shift
            ;;
        --deep|-d)
            DEPTH="deep"
            shift
            ;;
        --basic|-b)
            DEPTH="basic"
            shift
            ;;
        --json|-j)
            OUTPUT="json"
            shift
            ;;
        --console|-c)
            OUTPUT="console"
            shift
            ;;
        --skip-contracts)
            SKIP_CONTRACTS=true
            shift
            ;;
        --output-file)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --help|-h)
            show_usage
            exit 0
            ;;
        -*)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
        *)
            if [[ -z "$CONFIG_FILE" ]]; then
                CONFIG_FILE="$1"
            else
                print_error "Multiple config files specified"
                show_usage
                exit 1
            fi
            shift
            ;;
    esac
done

# Validate config file
if [[ -z "$CONFIG_FILE" ]]; then
    print_error "No configuration file specified"
    show_usage
    exit 1
fi

if [[ ! -f "$CONFIG_FILE" ]]; then
    print_error "Configuration file not found: $CONFIG_FILE"
    exit 1
fi

# Check if validator directory exists
if [[ ! -d "$VALIDATOR_DIR" ]]; then
    print_error "Validator directory not found: $VALIDATOR_DIR"
    exit 1
fi

# Convert config file to absolute path
if [[ ! "$CONFIG_FILE" = /* ]]; then
    CONFIG_FILE="$(pwd)/$CONFIG_FILE"
fi

# Convert output file to absolute path if provided
if [[ -n "$OUTPUT_FILE" && ! "$OUTPUT_FILE" = /* ]]; then
    OUTPUT_FILE="$(pwd)/$OUTPUT_FILE"
fi

# Show configuration
print_status "Starting Monitoring API Validation"
print_status "Config file: $CONFIG_FILE"
print_status "Mode: $MODE"
print_status "Depth: $DEPTH"
print_status "Output: $OUTPUT"
if [[ -n "$OUTPUT_FILE" ]]; then
    print_status "Output file: $OUTPUT_FILE"
fi

# Build validator command
VALIDATOR_CMD="cd $VALIDATOR_DIR && go run main.go -config $CONFIG_FILE -mode $MODE -depth $DEPTH -output $OUTPUT"

if [[ "$SKIP_CONTRACTS" == "true" ]]; then
    VALIDATOR_CMD="$VALIDATOR_CMD -skip-contracts"
fi

if [[ -n "$OUTPUT_FILE" ]]; then
    VALIDATOR_CMD="$VALIDATOR_CMD -output-file $OUTPUT_FILE"
fi

# Run validation
print_status "Executing: $VALIDATOR_CMD"
echo

if eval $VALIDATOR_CMD; then
    echo
    print_success "Validation completed successfully!"

    # Show output file location if specified
    if [[ -n "$OUTPUT_FILE" && -f "$OUTPUT_FILE" ]]; then
        print_success "Report saved to: $OUTPUT_FILE"

        # Show summary of JSON file if available and jq is installed
        if command -v jq >/dev/null 2>&1; then
            print_status "Report Summary:"
            jq -r '
                "Total Tests: " + (.summary.total_tests | tostring) + " | " +
                "Passed: " + (.summary.passed_tests | tostring) + " | " +
                "Failed: " + (.summary.failed_tests | tostring) + "\n" +
                "API Response Time Avg: " + (.summary.api_response_time_avg | tostring) + "ms\n" +
                "Redis Consistency: " + (.summary.redis_consistency * 100 | floor | tostring) + "%\n" +
                "Contract Consistency: " + (.summary.contract_consistency * 100 | floor | tostring) + "%"
            ' "$OUTPUT_FILE"
        fi
    fi
else
    echo
    print_error "Validation failed!"
    exit 1
fi