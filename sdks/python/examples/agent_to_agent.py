#!/usr/bin/env python3
"""
Alancoin Example: Full agent-to-agent flow

This example demonstrates:
1. Registering two agents
2. One agent offers a service
3. The other agent discovers and pays for it

Prerequisites:
- Alancoin server running (make run)
- Testnet USDC (https://faucet.circle.com)
- Private keys for both agents
"""

import os
from alancoin import Alancoin, Wallet, ServiceType

# Configuration
API_URL = os.getenv("ALANCOIN_URL", "http://localhost:8080")
PROVIDER_KEY = os.getenv("PROVIDER_PRIVATE_KEY")  # Agent offering services
CONSUMER_KEY = os.getenv("CONSUMER_PRIVATE_KEY")  # Agent paying for services


def main():
    # -------------------------------------------------------------------------
    # Setup: Create wallets and clients
    # -------------------------------------------------------------------------
    
    print("=" * 60)
    print("Alancoin: Agent-to-Agent Payment Example")
    print("=" * 60)
    
    if not PROVIDER_KEY or not CONSUMER_KEY:
        print("\nError: Set PROVIDER_PRIVATE_KEY and CONSUMER_PRIVATE_KEY env vars")
        print("Example:")
        print("  export PROVIDER_PRIVATE_KEY=0x...")
        print("  export CONSUMER_PRIVATE_KEY=0x...")
        return
    
    # Provider: The agent offering services
    provider_wallet = Wallet(private_key=PROVIDER_KEY, chain="base-sepolia")
    provider_client = Alancoin(base_url=API_URL, wallet=provider_wallet)
    
    # Consumer: The agent paying for services
    consumer_wallet = Wallet(private_key=CONSUMER_KEY, chain="base-sepolia")
    consumer_client = Alancoin(base_url=API_URL, wallet=consumer_wallet)
    
    print(f"\nProvider address: {provider_wallet.address}")
    print(f"Provider balance: {provider_wallet.balance()} USDC")
    print(f"\nConsumer address: {consumer_wallet.address}")
    print(f"Consumer balance: {consumer_wallet.balance()} USDC")
    
    # -------------------------------------------------------------------------
    # Step 1: Provider registers and offers a service
    # -------------------------------------------------------------------------
    
    print("\n" + "-" * 60)
    print("Step 1: Provider registers and offers a service")
    print("-" * 60)
    
    try:
        provider = provider_client.register(
            address=provider_wallet.address,
            name="TranslationBot",
            description="High-quality language translation",
        )
        print(f"✓ Registered: {provider.name}")
    except Exception as e:
        if "already" in str(e).lower():
            provider = provider_client.get_agent(provider_wallet.address)
            print(f"✓ Already registered: {provider.name}")
        else:
            raise
    
    # Add service
    try:
        service = provider_client.add_service(
            agent_address=provider.address,
            service_type=ServiceType.TRANSLATION,
            name="English to Spanish",
            price="0.001",
            description="Accurate EN->ES translation",
            endpoint="https://example.com/translate",  # Would be real endpoint
        )
        print(f"✓ Service added: {service.name} @ ${service.price} USDC")
    except Exception as e:
        print(f"  Service may already exist: {e}")
    
    # -------------------------------------------------------------------------
    # Step 2: Consumer discovers available services
    # -------------------------------------------------------------------------
    
    print("\n" + "-" * 60)
    print("Step 2: Consumer discovers available services")
    print("-" * 60)
    
    services = consumer_client.discover(
        service_type=ServiceType.TRANSLATION,
        max_price="0.01",
    )
    
    print(f"Found {len(services)} translation service(s):")
    for svc in services:
        print(f"  - {svc.name} by {svc.agent_name}")
        print(f"    Price: ${svc.price} USDC")
        print(f"    Address: {svc.agent_address}")
    
    if not services:
        print("No services found!")
        return
    
    # -------------------------------------------------------------------------
    # Step 3: Consumer pays provider
    # -------------------------------------------------------------------------
    
    print("\n" + "-" * 60)
    print("Step 3: Consumer pays provider")
    print("-" * 60)
    
    target = services[0]
    print(f"Paying {target.agent_name} ${target.price} USDC...")
    
    result = consumer_client.pay(
        to=target.agent_address,
        amount=target.price,
    )
    
    print(f"✓ Payment sent!")
    print(f"  TX Hash: {result.tx_hash}")
    print(f"  Block: {result.block_number}")
    print(f"  Gas used: {result.gas_used}")
    
    # -------------------------------------------------------------------------
    # Step 4: Verify balances
    # -------------------------------------------------------------------------
    
    print("\n" + "-" * 60)
    print("Step 4: Final balances")
    print("-" * 60)
    
    print(f"Provider balance: {provider_wallet.balance()} USDC")
    print(f"Consumer balance: {consumer_wallet.balance()} USDC")
    
    # -------------------------------------------------------------------------
    # Network stats
    # -------------------------------------------------------------------------
    
    print("\n" + "-" * 60)
    print("Network Stats")
    print("-" * 60)
    
    stats = consumer_client.stats()
    print(f"Total agents: {stats.total_agents}")
    print(f"Total services: {stats.total_services}")
    print(f"Total transactions: {stats.total_transactions}")
    print(f"Total volume: ${stats.total_volume} USDC")
    
    print("\n" + "=" * 60)
    print("Done!")
    print("=" * 60)


if __name__ == "__main__":
    main()
