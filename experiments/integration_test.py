#!/usr/bin/env python3
"""
Integration test: runs study1 --live against a real gateway server.

Prerequisites:
    1. make run                     # Start the platform
    2. Create a tenant + API key    # Via API or admin CLI
    3. Set environment variables:
       ALANCOIN_API_URL=http://localhost:8080
       ALANCOIN_API_KEY=sk_...

Usage:
    python integration_test.py
    ALANCOIN_API_URL=http://localhost:8080 ALANCOIN_API_KEY=sk_test python integration_test.py
"""

import asyncio
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path

import requests

sys.path.insert(0, str(Path(__file__).parent))

from harness.config import ExperimentConfig
from harness.runners import run_study1
from harness.clients.gateway_market import GatewayMarket, GatewayError


def check_server(api_url: str) -> bool:
    """Check if the gateway server is reachable."""
    try:
        resp = requests.get(f"{api_url}/health", timeout=5)
        return resp.status_code == 200
    except requests.ConnectionError:
        return False


def main():
    print("=" * 60)
    print("INTEGRATION TEST: Live Gateway")
    print("=" * 60)

    api_url = os.environ.get("ALANCOIN_API_URL", "http://localhost:8080")
    api_key = os.environ.get("ALANCOIN_API_KEY", "")

    errors = []

    # Pre-flight check
    print(f"\nChecking server at {api_url}...")
    if not check_server(api_url):
        print(f"  Server not reachable at {api_url}")
        print(f"  Start it with: make run")
        print(f"  Skipping integration test (not a failure).")
        sys.exit(0)
    print(f"  [OK] Server reachable")

    if not api_key:
        print(f"  No API key set (ALANCOIN_API_KEY)")
        print(f"  Skipping integration test (not a failure).")
        sys.exit(0)
    print(f"  [OK] API key configured")

    tmpdir = Path(tempfile.mkdtemp(prefix="harness_integration_"))

    try:
        # Test 1: GatewayMarket session lifecycle
        print(f"\n--- Test 1: Gateway session lifecycle ---")
        gw = GatewayMarket(api_url=api_url, api_key=api_key)
        buyer = gw.create_agent("IntegrationBuyer", "buyer", balance=10.0, max_total=10.0)

        session = gw.setup_session(buyer.id)
        print(f"  [OK] Session created: {session.session_id}")
        assert session.session_id.startswith("gw_"), f"Unexpected session ID format: {session.session_id}"

        teardown_result = gw.teardown_session(buyer.id)
        print(f"  [OK] Session closed, spent={teardown_result.get('totalSpent', '?')}")

        # Test 2: Run study1 via live gateway
        print(f"\n--- Test 2: Study 1 via live gateway ---")
        config = ExperimentConfig(random_seed=42, mock_mode=False)
        results = asyncio.run(run_study1(
            config=config,
            output_dir=tmpdir,
            mock_mode=True,  # Mock LLM, but live market
            condition_filter="monopoly_cba",
            max_runs=1,
            live_mode=True,
            api_url=api_url,
            api_key=api_key,
        ))

        if not results.runs:
            errors.append("No runs returned from live study1")
        else:
            print(f"  [OK] {len(results.runs)} run(s) completed")
            run = results.runs[0]
            print(f"  Condition: {run.condition}")
            print(f"  Transactions: {run.transactions_accepted}/{run.transactions_attempted}")

        # Test 3: Verify output files
        print(f"\n--- Test 3: Output files ---")
        jsonl_files = list(tmpdir.glob("*_runs.jsonl"))
        if not jsonl_files:
            errors.append("No runs.jsonl from live run")
        else:
            print(f"  [OK] runs.jsonl exists")

    except GatewayError as e:
        errors.append(f"Gateway error: {e}")
        import traceback
        traceback.print_exc()
    except Exception as e:
        errors.append(f"Exception: {e}")
        import traceback
        traceback.print_exc()
    finally:
        shutil.rmtree(tmpdir, ignore_errors=True)

    # Report
    print()
    if errors:
        print("INTEGRATION TEST FAILED:")
        for err in errors:
            print(f"  - {err}")
        sys.exit(1)
    else:
        print("INTEGRATION TEST PASSED")
        sys.exit(0)


if __name__ == "__main__":
    main()
