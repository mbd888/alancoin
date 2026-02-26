#!/usr/bin/env python3
"""
Alancoin Example: Full agent-to-agent flow

This example demonstrates:
1. Registering two agents (provider and consumer)
2. Provider offers a translation service
3. Consumer discovers services and pays via gateway session

Prerequisites:
- Alancoin server running (make run)
- Agent addresses and API keys for both agents

Setup:
  export PROVIDER_ADDRESS=0x...
  export PROVIDER_API_KEY=ak_...
  export CONSUMER_ADDRESS=0x...
  export CONSUMER_API_KEY=ak_...

Run: python agent_to_agent.py
"""

import os
from alancoin import connect, spend
from alancoin.admin import Alancoin
from alancoin.models import ServiceType

# Configuration
API_URL = os.getenv("ALANCOIN_URL", "http://localhost:8080")
PROVIDER_ADDRESS = os.getenv("PROVIDER_ADDRESS")
PROVIDER_API_KEY = os.getenv("PROVIDER_API_KEY")
CONSUMER_ADDRESS = os.getenv("CONSUMER_ADDRESS")
CONSUMER_API_KEY = os.getenv("CONSUMER_API_KEY")


def main():
    print("=" * 60)
    print("Alancoin: Agent-to-Agent Payment Example")
    print("=" * 60)

    if not PROVIDER_ADDRESS or not CONSUMER_ADDRESS:
        print("\nError: Set agent addresses and API keys.")
        print("Example:")
        print("  export PROVIDER_ADDRESS=0x...")
        print("  export PROVIDER_API_KEY=ak_...")
        print("  export CONSUMER_ADDRESS=0x...")
        print("  export CONSUMER_API_KEY=ak_...")
        return

    # -------------------------------------------------------------------------
    # Setup: Create clients
    # -------------------------------------------------------------------------

    provider_client = Alancoin(
        base_url=API_URL, api_key=PROVIDER_API_KEY, address=PROVIDER_ADDRESS,
    )
    consumer_client = Alancoin(
        base_url=API_URL, api_key=CONSUMER_API_KEY, address=CONSUMER_ADDRESS,
    )

    # -------------------------------------------------------------------------
    # Step 1: Register agents (skip if API keys already provided)
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Step 1: Register agents")
    print("-" * 60)

    for name, client, addr in [
        ("TranslationBot", provider_client, PROVIDER_ADDRESS),
        ("ConsumerAgent", consumer_client, CONSUMER_ADDRESS),
    ]:
        try:
            result = client.register(address=addr, name=name, description=f"{name} demo agent")
            api_key = result.get("apiKey", "")
            print(f"   Registered {name}: save key! {api_key[:20]}...")
        except Exception as e:
            if "exists" in str(e).lower():
                print(f"   {name} already registered")
            else:
                raise

    # -------------------------------------------------------------------------
    # Step 2: Provider offers a service
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Step 2: Provider offers a translation service")
    print("-" * 60)

    try:
        service = provider_client.add_service(
            agent_address=PROVIDER_ADDRESS,
            service_type=ServiceType.TRANSLATION,
            name="English to Spanish",
            price="0.001",
            description="Accurate EN->ES translation",
            endpoint="https://example.com/translate",
        )
        print(f"   Service added: {service.name} @ ${service.price} USDC")
    except Exception as e:
        print(f"   Service may already exist: {e}")

    # -------------------------------------------------------------------------
    # Step 3: Consumer discovers available services
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Step 3: Consumer discovers translation services")
    print("-" * 60)

    services = consumer_client.discover(
        service_type=ServiceType.TRANSLATION,
        max_price="0.01",
    )

    print(f"   Found {len(services)} translation service(s):")
    for svc in services:
        print(f"   - {svc.name} by {svc.agent_name}")
        print(f"     Price: ${svc.price} USDC | Reputation: {svc.reputation_tier}")

    if not services:
        print("   No services found!")
        return

    # -------------------------------------------------------------------------
    # Step 4: Consumer pays via gateway session
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Step 4: Consumer pays provider via gateway")
    print("-" * 60)

    # Option A: One-shot call with spend()
    print("\n   Using spend() for a one-shot call:")
    try:
        result = spend(
            API_URL,
            api_key=CONSUMER_API_KEY,
            service_type="translation",
            text="Hello, how are you?",
            target_language="es",
        )
        print(f"   Result: {result.get('output', result)}")
        print(f"   Cost: ${result.get('_gateway', {}).get('amountPaid', '?')} USDC")
    except Exception as e:
        print(f"   spend() error (expected if no real endpoint): {e}")

    # Option B: Multi-call session with connect()
    print("\n   Using connect() for a session with budget:")
    try:
        with connect(API_URL, api_key=CONSUMER_API_KEY, budget="1.00") as gw:
            result = gw.call("translation", text="Good morning", target_language="es")
            print(f"   Call 1 result: {result.get('output', result)}")
            print(f"   Spent so far: ${gw.total_spent}")
            print(f"   Remaining: ${gw.remaining}")
    except Exception as e:
        print(f"   connect() error (expected if no real endpoint): {e}")

    # -------------------------------------------------------------------------
    # Step 5: Check balances
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Step 5: Final balances")
    print("-" * 60)

    for label, client, addr in [
        ("Provider", provider_client, PROVIDER_ADDRESS),
        ("Consumer", consumer_client, CONSUMER_ADDRESS),
    ]:
        balance = client.get_platform_balance(addr)
        available = balance.get("balance", {}).get("available", "?")
        print(f"   {label}: ${available} USDC")

    # -------------------------------------------------------------------------
    # Network stats
    # -------------------------------------------------------------------------

    print("\n" + "-" * 60)
    print("Network Stats")
    print("-" * 60)

    stats = consumer_client.stats()
    print(f"   Total agents: {stats.total_agents}")
    print(f"   Total services: {stats.total_services}")
    print(f"   Total transactions: {stats.total_transactions}")
    print(f"   Total volume: ${stats.total_volume} USDC")

    print("\n" + "=" * 60)
    print("Done!")
    print("=" * 60)


if __name__ == "__main__":
    main()
