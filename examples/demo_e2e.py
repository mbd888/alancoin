#!/usr/bin/env python3
"""
Alancoin End-to-End Demo

Demonstrates the full agent-to-agent loop:
1. Start service agents (translator, echo)
2. Register a buyer agent with platform balance
3. Buyer discovers and calls services through the gateway
4. Show real payments flowing through the platform

Prerequisites:
    - Alancoin platform running: make run   (localhost:8080)
    - Python SDK available:      pip install -e sdks/python/

Usage:
    python examples/demo_e2e.py
"""
import sys
import os
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdks", "python"))

from alancoin import Alancoin
from alancoin.serve import ServiceAgent
from alancoin.session_keys import generate_session_keypair

PLATFORM_URL = os.environ.get("ALANCOIN_URL", "http://localhost:8080")

# -----------------------------------------------------------------------------
# Step 1: Define service agents
# -----------------------------------------------------------------------------

translator = ServiceAgent(
    name="TranslatorBot",
    base_url=PLATFORM_URL,
    description="Translates text between languages",
)

echo_bot = ServiceAgent(
    name="EchoBot",
    base_url=PLATFORM_URL,
    description="Echoes back your input",
)


@translator.service("translation", price="0.005", description="Translate text")
def translate(text="", target="es"):
    table = {
        "es": {"hello": "hola", "world": "mundo", "how": "como", "are": "estas",
               "you": "tu", "good": "bueno", "morning": "manana", "the": "el",
               "is": "es", "thank": "gracias"},
        "fr": {"hello": "bonjour", "world": "monde", "how": "comment",
               "are": "etes", "you": "vous", "good": "bon", "morning": "matin",
               "the": "le", "is": "est", "thank": "merci"},
    }
    words = text.split()
    lang_table = table.get(target, {})
    translated = []
    for w in words:
        clean = w.lower().strip(".,!?")
        punct = w[len(clean):] if len(clean) < len(w) else ""
        result = lang_table.get(clean, w)
        if w and w[0].isupper():
            result = result.capitalize()
        translated.append(result + punct)
    return {"output": " ".join(translated), "target": target}


@echo_bot.service("echo", price="0.001", description="Echo your input")
def echo(text="", **kwargs):
    return {"output": text, "echoed": True}


# -----------------------------------------------------------------------------
# Step 2: Setup
# -----------------------------------------------------------------------------

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


def register_buyer():
    """Register a buyer agent and fund it with platform balance."""
    _, buyer_addr = generate_session_keypair()
    client = Alancoin(base_url=PLATFORM_URL)

    result = client.register(
        address=buyer_addr,
        name="BuyerAgent",
        description="Demo buyer that discovers and calls services",
    )
    api_key = result.get("apiKey")

    # Fund the buyer's platform balance via admin deposit endpoint
    funded_client = Alancoin(base_url=PLATFORM_URL, api_key=api_key)
    try:
        funded_client._request(
            "POST",
            "/v1/admin/deposits",
            json={
                "agentAddress": buyer_addr,
                "amount": "10.00",
                "txHash": f"0xdemo_{int(time.time())}",
            },
        )
        print(f"  Funded with $10.00 USDC platform balance")
    except Exception as e:
        print(f"  Note: Could not fund buyer: {e}")

    return buyer_addr, api_key


# -----------------------------------------------------------------------------
# Step 3: Run the demo
# -----------------------------------------------------------------------------

def run_demo():
    print("=" * 60)
    print("  ALANCOIN END-TO-END DEMO")
    print("  Autonomous agents discovering and paying each other")
    print("=" * 60)
    print()

    # Check platform
    print("[1/5] Checking platform...")
    if not check_platform():
        sys.exit(1)
    print("  Platform is running")
    print()

    # Start service agents in background
    print("[2/5] Starting service agents...")
    translator.start(port=5001)
    echo_bot.start(port=5002)
    time.sleep(1)  # Let them register

    print(f"  TranslatorBot @ localhost:5001 (address: {translator.address})")
    print(f"  EchoBot       @ localhost:5002 (address: {echo_bot.address})")
    print()

    # Register buyer
    print("[3/5] Registering buyer agent...")
    buyer_addr, api_key = register_buyer()
    print(f"  BuyerAgent registered: {buyer_addr}")
    print()

    # Create gateway session and call services
    print("[4/5] Buyer creating gateway session...")
    client = Alancoin(base_url=PLATFORM_URL, api_key=api_key)

    print("  Budget: max $1.00 total, expires in 1h")
    print()

    print("[5/5] Calling services through the gateway...")
    print("-" * 60)

    with client.gateway(max_total="1.00") as gw:
        # Call 1: Translate "Hello world" to Spanish
        print()
        print("  >> Gateway call: translate 'Hello world' to Spanish")
        result = gw.call("translation", text="Hello world", target="es")
        meta = result.get("_gateway", {})
        print(f"  << Result: {result.get('output')}")
        print(f"     Paid: ${meta.get('amountPaid')} to {meta.get('serviceName')}")
        print(f"     Spent so far: ${gw.total_spent}, Remaining: ${gw.remaining}")

        # Call 2: Translate "Good morning" to French
        print()
        print("  >> Gateway call: translate 'Good morning' to French")
        result = gw.call("translation", text="Good morning", target="fr")
        meta = result.get("_gateway", {})
        print(f"  << Result: {result.get('output')}")
        print(f"     Paid: ${meta.get('amountPaid')} to {meta.get('serviceName')}")
        print(f"     Spent so far: ${gw.total_spent}, Remaining: ${gw.remaining}")

        # Call 3: Echo
        print()
        print("  >> Gateway call: echo 'Bounded autonomy works!'")
        result = gw.call("echo", text="Bounded autonomy works!")
        meta = result.get("_gateway", {})
        print(f"  << Result: {result.get('output')}")
        print(f"     Paid: ${meta.get('amountPaid')} to {meta.get('serviceName')}")
        print(f"     Spent so far: ${gw.total_spent}, Remaining: ${gw.remaining}")

    print()
    print("-" * 60)

    # Test 402 (no payment proof) - raw HTTP to show the protocol
    import requests
    print()
    print("  >> Calling TranslatorBot WITHOUT payment (402 test)...")
    try:
        resp = requests.post(
            "http://localhost:5001/services/translation",
            json={"text": "Should fail"},
            timeout=10,
        )
        result = resp.json()
        print(f"  << 402 Payment Required: ${result['price']} {result['currency']}")
        print(f"     Recipient: {result['recipient']}")
    except Exception as e:
        print(f"  << Error: {e}")

    print()

    # Show platform stats
    try:
        stats_client = Alancoin(base_url=PLATFORM_URL)
        stats = stats_client.stats()
        print(f"  Platform Stats:")
        print(f"    Agents:       {stats.total_agents}")
        print(f"    Services:     {stats.total_services}")
        print(f"    Transactions: {stats.total_transactions}")
        print(f"    Volume:       ${stats.total_volume} USDC")
    except Exception:
        pass

    print()
    print("=" * 60)
    print("  Demo complete. Service agents still running.")
    print(f"  Dashboard: {PLATFORM_URL}")
    print("  Press Ctrl+C to stop.")
    print("=" * 60)

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        translator.stop()
        echo_bot.stop()


if __name__ == "__main__":
    run_demo()
