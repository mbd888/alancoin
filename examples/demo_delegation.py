#!/usr/bin/env python3
"""
Alancoin Gateway Delegation Demo

Demonstrates the gateway-based workflow:
1. Register a buyer agent and fund it via admin deposit
2. Register a seller agent with a translation service
3. Use connect() to open a gateway session and call the service
4. Show spending summary

Prerequisites:
    - Alancoin platform running: make run   (localhost:8080)
    - Python SDK available:      pip install -e sdks/python/

Usage:
    python examples/demo_delegation.py
"""
import sys
import os
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdks", "python"))

from alancoin import connect
from alancoin.admin import Alancoin

PLATFORM_URL = os.environ.get("ALANCOIN_URL", "http://localhost:8080")


def check_platform():
    """Verify the platform is running."""
    client = Alancoin(base_url=PLATFORM_URL)
    try:
        client.health()
        return True
    except Exception as e:
        print(f"  Platform not reachable at {PLATFORM_URL}: {e}")
        print(f"  Start it with: make run")
        return False


def setup_seller(admin):
    """Register a seller agent with a translation service."""
    seller_addr = f"0xseller_{int(time.time())}"
    result = admin.register(
        address=seller_addr,
        name="TranslatorBot",
        description="Translates text between languages",
    )
    admin.add_service(
        agent_address=seller_addr,
        service_type="translation",
        name="English to Spanish",
        price="0.005",
        description="Word-level translation",
    )
    return seller_addr


def setup_buyer(admin):
    """Register and fund a buyer agent."""
    buyer_addr = f"0xbuyer_{int(time.time())}"
    result = admin.register(
        address=buyer_addr,
        name="DelegationBuyer",
        description="Demo buyer using gateway",
    )
    api_key = result.get("apiKey")

    # Fund via admin deposit
    funded = Alancoin(base_url=PLATFORM_URL, api_key=api_key)
    try:
        funded._request(
            "POST",
            "/v1/admin/deposits",
            json={
                "agentAddress": buyer_addr,
                "amount": "10.00",
                "txHash": f"0xdemo_delegation_{int(time.time())}",
            },
        )
        print(f"  Funded with $10.00 USDC platform balance")
    except Exception as e:
        print(f"  Note: Could not fund buyer: {e}")

    return buyer_addr, api_key


def run_demo():
    print("=" * 70)
    print("  ALANCOIN GATEWAY DEMO")
    print("  The 'with' block is the entire product")
    print("=" * 70)
    print()

    # Check platform
    print("[1/4] Checking platform...")
    if not check_platform():
        sys.exit(1)
    print("  Platform is running")
    print()

    admin = Alancoin(base_url=PLATFORM_URL)

    # Setup seller
    print("[2/4] Registering seller agent...")
    seller_addr = setup_seller(admin)
    print(f"  TranslatorBot: {seller_addr}")
    print()

    # Setup buyer
    print("[3/4] Registering and funding buyer agent...")
    buyer_addr, api_key = setup_buyer(admin)
    print(f"  DelegationBuyer: {buyer_addr}")
    print()

    # Use connect() for the gateway session
    print("[4/4] Calling services via gateway...")
    print("-" * 70)

    with connect(PLATFORM_URL, api_key=api_key, budget="5.00") as gw:
        # Call 1: translation
        try:
            result = gw.call("translation", text="Hello world", target="es")
            print(f"  Translation: {result.get('output', result)}")
        except Exception as e:
            print(f"  Translation error: {e}")

        # Call 2: another translation
        try:
            result = gw.call("translation", text="The document is a summary", target="es")
            print(f"  Translation: {result.get('output', result)}")
        except Exception as e:
            print(f"  Translation error: {e}")

        print()
        print(f"  Total spent: ${gw.total_spent}")
        print(f"  Remaining:   ${gw.remaining}")
        print(f"  Requests:    {gw.request_count}")

    print()
    print("-" * 70)
    print()
    print("  SDK usage:")
    print()
    print("    from alancoin import connect")
    print()
    print('    with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:')
    print('        result = gw.call("translation", text="Hello", target="es")')
    print('        print(result["output"])')
    print()
    print("=" * 70)
    print("  Demo complete.")
    print("=" * 70)


if __name__ == "__main__":
    run_demo()
