#!/usr/bin/env python3
"""
Automated analysis script to compare active epochs with their status using NEW protocol/market addresses.

This script queries the Monitor API to:
1. Get active epochs (using OLD or default protocol/market addresses)
2. Check their detailed status (using NEW protocol/market addresses where relayer-py writes)
3. Analyze transaction hash presence and identify issues

IMPORTANT: Addresses are automatically lowercased to match Redis key format (relayer-py lowercases addresses).

Usage:
    python3 scripts/analyze_epoch_status.py \
        --api-url http://localhost:9092 \
        --active-protocol 0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401 \
        --active-market 0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d \
        --status-protocol 0xC9e7304f719D35919b0371d8B242ab59E0966d63 \
        --status-market 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f
"""

import argparse
import json
import sys
import requests
from typing import Dict, List, Optional
from collections import defaultdict


def normalize_address(addr: str) -> str:
    """Normalize Ethereum address to lowercase (matching Redis key format)."""
    if addr.startswith("0x") or addr.startswith("0X"):
        return addr.lower()
    return addr.lower()


def get_active_epochs(api_url: str, protocol: Optional[str] = None, market: Optional[str] = None) -> List[Dict]:
    """Get active epochs from API."""
    url = f"{api_url}/api/v1/epochs/active"
    params = {}
    if protocol:
        params["protocol"] = normalize_address(protocol)
    if market:
        params["market"] = normalize_address(market)
    
    try:
        response = requests.get(url, params=params, timeout=10)
        response.raise_for_status()
        return response.json()
    except Exception as e:
        print(f"ERROR: Failed to get active epochs: {e}", file=sys.stderr)
        return []


def get_epoch_status(api_url: str, epoch_id: str, protocol: str, market: str) -> Optional[Dict]:
    """Get epoch status from API using NEW addresses (lowercased)."""
    url = f"{api_url}/api/v1/epochs/{epoch_id}/status"
    params = {
        "protocol": normalize_address(protocol),
        "market": normalize_address(market)
    }
    
    try:
        response = requests.get(url, params=params, timeout=10)
        if response.status_code == 404:
            return None
        response.raise_for_status()
        return response.json()
    except Exception as e:
        print(f"ERROR: Failed to get status for epoch {epoch_id}: {e}", file=sys.stderr)
        return None


def analyze_epoch(epoch: Dict, status: Optional[Dict]) -> Dict:
    """Analyze a single epoch and its status."""
    analysis = {
        "epoch_id": epoch.get("epoch_id"),
        "phase": epoch.get("phase"),
        "status": epoch.get("status"),
        "has_status_data": status is not None,
        "onchain_status": None,
        "onchain_tx_hash": None,
        "has_tx_hash": False,
        "onchain_block_number": None,
        "onchain_error": None,
        "level1_status": None,
        "level2_status": None,
        "priority": None,
        "vpa_submission_attempted": None,
        "issues": [],
    }
    
    if status and "state" in status:
        state = status["state"]
        analysis["onchain_status"] = state.get("onchain_status")
        analysis["onchain_tx_hash"] = state.get("onchain_tx_hash")
        analysis["has_tx_hash"] = bool(analysis["onchain_tx_hash"] and analysis["onchain_tx_hash"].strip())
        analysis["onchain_block_number"] = state.get("onchain_block_number")
        analysis["level1_status"] = state.get("level1_status")
        analysis["level2_status"] = state.get("level2_status")
        analysis["priority"] = state.get("priority")
        analysis["vpa_submission_attempted"] = state.get("vpa_submission_attempted")
        analysis["onchain_error"] = state.get("onchain_error") or state.get("error_message") or state.get("error")
        
        # Detect issues
        if analysis["onchain_status"] == "failed":
            analysis["issues"].append("On-chain submission failed")
            if not analysis["onchain_error"]:
                analysis["issues"].append("Missing error details in Redis state")
        
        # Critical: Check for missing transaction hashes
        if analysis["onchain_status"] in ["submitted", "confirmed", "failed"]:
            if not analysis["has_tx_hash"]:
                analysis["issues"].append(f"onchain_status={analysis['onchain_status']} but NO tx_hash")
        
        if epoch.get("phase") == "onchain_submission":
            if not analysis["has_tx_hash"]:
                analysis["issues"].append("Phase is onchain_submission but no tx_hash")
            if analysis["onchain_status"] not in ["submitted", "confirmed", "queued", "failed"]:
                analysis["issues"].append(f"Phase is onchain_submission but onchain_status={analysis['onchain_status']}")
        
        if analysis["vpa_submission_attempted"]:
            if analysis["onchain_status"] not in ["submitted", "confirmed", "queued", "failed"]:
                analysis["issues"].append(f"VPA attempted but onchain_status is {analysis['onchain_status']}")
            if analysis["onchain_status"] in ["submitted", "confirmed", "failed"] and not analysis["has_tx_hash"]:
                analysis["issues"].append(f"VPA attempted, status={analysis['onchain_status']} but NO tx_hash")
    else:
        analysis["issues"].append("No status data found with NEW addresses")
    
    return analysis


def main():
    parser = argparse.ArgumentParser(
        description="Analyze active epochs vs their status with transaction hash tracking",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Basic usage with NEW addresses
  python3 scripts/analyze_epoch_status.py \\
    --status-protocol 0xC9e7304f719D35919b0371d8B242ab59E0966d63 \\
    --status-market 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f

  # Full usage with both OLD and NEW addresses
  python3 scripts/analyze_epoch_status.py \\
    --api-url http://localhost:9092 \\
    --active-protocol 0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401 \\
    --active-market 0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d \\
    --status-protocol 0xC9e7304f719D35919b0371d8B242ab59E0966d63 \\
    --status-market 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f \\
    --output table
        """
    )
    parser.add_argument("--api-url", default="http://localhost:9092", help="Monitor API URL")
    parser.add_argument("--active-protocol", help="Protocol address for active epochs query (default: use API defaults)")
    parser.add_argument("--active-market", help="Market address for active epochs query (default: use API defaults)")
    parser.add_argument("--status-protocol", required=True, help="NEW protocol state contract address for status queries (where relayer-py writes)")
    parser.add_argument("--status-market", required=True, help="Data market address for status queries (where relayer-py writes)")
    parser.add_argument("--output", choices=["json", "table", "summary"], default="summary", help="Output format")
    
    args = parser.parse_args()
    
    print(f"Fetching active epochs (protocol={args.active_protocol or 'default'}, market={args.active_market or 'default'})...")
    active_epochs = get_active_epochs(args.api_url, args.active_protocol, args.active_market)
    
    if not active_epochs:
        print("No active epochs found", file=sys.stderr)
        sys.exit(1)
    
    print(f"Found {len(active_epochs)} active epochs")
    print(f"Checking status with NEW addresses: protocol={args.status_protocol.lower()}, market={args.status_market.lower()}...")
    
    analyses = []
    for epoch in active_epochs:
        epoch_id = epoch.get("epoch_id")
        status = get_epoch_status(args.api_url, epoch_id, args.status_protocol, args.status_market)
        analysis = analyze_epoch(epoch, status)
        analyses.append(analysis)
    
    # Summary statistics
    stats = {
        "total_epochs": len(analyses),
        "with_status_data": sum(1 for a in analyses if a["has_status_data"]),
        "without_status_data": sum(1 for a in analyses if not a["has_status_data"]),
        "onchain_statuses": defaultdict(int),
        "failed_count": 0,
        "failed_without_error": 0,
        "with_tx_hash": 0,
        "without_tx_hash": 0,
        "statuses_without_tx_hash": defaultdict(int),
        "issues_count": defaultdict(int),
    }
    
    for analysis in analyses:
        if analysis["onchain_status"]:
            stats["onchain_statuses"][analysis["onchain_status"]] += 1
            if analysis["onchain_status"] == "failed":
                stats["failed_count"] += 1
                if not analysis["onchain_error"]:
                    stats["failed_without_error"] += 1
            
            # Track transaction hash presence by status
            if analysis["has_tx_hash"]:
                stats["with_tx_hash"] += 1
            else:
                stats["without_tx_hash"] += 1
                if analysis["onchain_status"] in ["submitted", "confirmed", "failed"]:
                    stats["statuses_without_tx_hash"][analysis["onchain_status"]] += 1
        
        for issue in analysis["issues"]:
            stats["issues_count"][issue] += 1
    
    if args.output == "json":
        print(json.dumps({
            "analyses": analyses,
            "stats": dict(stats),
        }, indent=2))
    elif args.output == "table":
        print("\nEpoch Analysis:")
        print(f"{'Epoch ID':<12} {'Phase':<20} {'Onchain Status':<15} {'Has TX Hash':<12} {'Has Error':<10} {'Issues':<50}")
        print("-" * 130)
        for a in analyses:
            has_tx = "Yes" if a["has_tx_hash"] else "No"
            has_error = "Yes" if a["onchain_error"] else "No"
            issues_str = ", ".join(a["issues"])[:50] if a["issues"] else "None"
            print(f"{a['epoch_id']:<12} {a['phase']:<20} {str(a['onchain_status']):<15} {has_tx:<12} {has_error:<10} {issues_str:<50}")
    else:  # summary
        print("\n" + "="*80)
        print("SUMMARY")
        print("="*80)
        print(f"Total active epochs: {stats['total_epochs']}")
        print(f"With status data (NEW addresses): {stats['with_status_data']}")
        print(f"Without status data: {stats['without_status_data']}")
        print(f"\nOn-chain Status Distribution:")
        for status, count in sorted(stats["onchain_statuses"].items()):
            print(f"  {status}: {count}")
        
        print(f"\nTransaction Hash Analysis:")
        print(f"  Epochs WITH tx_hash: {stats['with_tx_hash']}")
        print(f"  Epochs WITHOUT tx_hash: {stats['without_tx_hash']}")
        if stats["statuses_without_tx_hash"]:
            print(f"\n  ⚠️  Statuses WITHOUT tx_hash (CRITICAL):")
            for status, count in sorted(stats["statuses_without_tx_hash"].items()):
                print(f"    {status}: {count} epochs")
        
        print(f"\nFailed Submissions: {stats['failed_count']}")
        print(f"Failed without error details: {stats['failed_without_error']}")
        
        # Show epochs without tx_hash that should have one
        missing_tx_epochs = [a for a in analyses if a["onchain_status"] in ["submitted", "confirmed", "failed"] and not a["has_tx_hash"]]
        if missing_tx_epochs:
            print(f"\n⚠️  CRITICAL: Epochs with status requiring tx_hash but missing it:")
            for a in missing_tx_epochs[:10]:  # Show first 10
                print(f"  Epoch {a['epoch_id']}: status={a['onchain_status']}, phase={a['phase']}, priority={a['priority']}")
            if len(missing_tx_epochs) > 10:
                print(f"  ... and {len(missing_tx_epochs) - 10} more")
        
        if stats["issues_count"]:
            print(f"\nIssues Found:")
            for issue, count in sorted(stats["issues_count"].items(), key=lambda x: x[1], reverse=True):
                print(f"  {issue}: {count}")
        
        # Show failed epochs
        failed_epochs = [a for a in analyses if a["onchain_status"] == "failed"]
        if failed_epochs:
            print(f"\nFailed Epochs:")
            for a in failed_epochs:
                print(f"  Epoch {a['epoch_id']}: {a['onchain_error'] or 'No error details'}")
    
    # Exit with error if there are critical issues
    if stats["failed_without_error"] > 0 or stats["statuses_without_tx_hash"]:
        exit_code = 1
        if stats["failed_without_error"] > 0:
            print(f"\n⚠️  WARNING: {stats['failed_without_error']} failed epochs without error details", file=sys.stderr)
        if stats["statuses_without_tx_hash"]:
            print(f"\n⚠️  WARNING: {sum(stats['statuses_without_tx_hash'].values())} epochs with status requiring tx_hash but missing it", file=sys.stderr)
        sys.exit(exit_code)


if __name__ == "__main__":
    main()

