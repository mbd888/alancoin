#!/usr/bin/env python3
"""
Alancoin: Complete End-to-End Demo

This shows the full money flow:
1. Get platform deposit address
2. Register agent
3. Deposit USDC (agent sends to platform)
4. Check platform balance
5. Create session key with limits
6. Spend via session key
7. Withdraw remaining balance

Setup:
  export AGENT_PRIVATE_KEY=0x...  # Your testnet wallet
  # Get testnet USDC at: https://faucet.circle.com

Run: python full_demo.py
"""

import os
import time
from alancoin import Alancoin, Wallet
from alancoin.session_keys import SessionKeyManager

API_URL = os.getenv("ALANCOIN_URL", "http://localhost:8080")
PRIVATE_KEY = os.getenv("AGENT_PRIVATE_KEY")


def main():
    print("=" * 60)
    print("Alancoin: Complete Money Flow Demo")
    print("=" * 60)

    if not PRIVATE_KEY:
        print("\nSet AGENT_PRIVATE_KEY env var")
        print("  export AGENT_PRIVATE_KEY=0x...")
        return

    # Setup
    wallet = Wallet(private_key=PRIVATE_KEY, chain="base-sepolia")
    client = Alancoin(base_url=API_URL, wallet=wallet)

    print(f"\n👤 Your wallet: {wallet.address}")
    print(f"   On-chain USDC: {wallet.balance()} USDC")

    # Step 1: Get platform info
    print("\n" + "-" * 60)
    print("1. Get platform deposit address")
    print("-" * 60)

    platform_info = client.get_platform_info()
    deposit_addr = platform_info['platform']['depositAddress']
    chain = platform_info['platform']['chain']
    
    print(f"\n   Deposit address: {deposit_addr}")
    print(f"   Chain: {chain}")
    print(f"\n   To deposit: Send USDC to {deposit_addr}")

    # Step 2: Register agent
    print("\n" + "-" * 60)
    print("2. Register agent")
    print("-" * 60)

    try:
        result = client.register(
            address=wallet.address,
            name="FullDemoAgent",
            description="End-to-end demo agent",
        )
        api_key = result.get('apiKey', '')
        print(f"✓ Registered: {result['agent']['name']}")
        print(f"  API Key: {api_key[:20]}...")
    except Exception as e:
        if "exists" in str(e).lower():
            print("✓ Already registered")
        else:
            raise

    # Step 3: Check platform balance
    print("\n" + "-" * 60)
    print("3. Check platform balance")
    print("-" * 60)

    balance = client.get_platform_balance(wallet.address)
    available = balance['balance']['available']
    print(f"\n   Platform balance: ${available} USDC")

    if float(available) < 0.1:
        print(f"\n   ⚠️  Low platform balance!")
        print(f"   Send USDC to: {deposit_addr}")
        print(f"   Then wait ~30 seconds for auto-detection")
        print(f"\n   (Skipping spend/withdraw demo due to low balance)")
        return

    # Step 4: Create session key
    print("\n" + "-" * 60)
    print("4. Create session key with spending limits")
    print("-" * 60)

    skm = SessionKeyManager()
    key = client.create_session_key(
        agent_address=wallet.address,
        public_key=skm.public_key,
        expires_in="1h",
        max_per_transaction="0.50",
        max_per_day="2.00",
    )
    skm.set_key_id(key["id"])

    print(f"\n✓ Session key created: {key['id'][:20]}...")
    print(f"  Limits: $0.50/tx, $2.00/day")

    # Step 5: Spend via session key
    print("\n" + "-" * 60)
    print("5. Spend via session key")
    print("-" * 60)

    # Use a test recipient (could be another agent)
    recipient = "0x742d35Cc6634C0532925a3b844Bc9e7595f8fE00"
    amount = "0.05"

    print(f"\n   Sending ${amount} to {recipient[:15]}...")

    try:
        result = skm.transact(
            client=client,
            agent_address=wallet.address,
            to=recipient,
            amount=amount,
        )
        
        status = result.get("status", "unknown")
        print(f"\n✓ Transaction {status}!")
        
        if result.get("txHash"):
            print(f"  TX: https://sepolia.basescan.org/tx/{result['txHash']}")
        
    except Exception as e:
        print(f"  Error: {e}")

    # Step 6: Check updated balance
    print("\n" + "-" * 60)
    print("6. Check updated balance")
    print("-" * 60)

    balance = client.get_platform_balance(wallet.address)
    print(f"\n   Platform balance: ${balance['balance']['available']} USDC")
    print(f"   Total spent: ${balance['balance']['totalOut']} USDC")

    # Step 7: View ledger history
    print("\n" + "-" * 60)
    print("7. View ledger history")
    print("-" * 60)

    history = client.get_ledger_history(wallet.address)
    entries = history.get('entries', [])
    
    if entries:
        print(f"\n   Recent transactions:")
        for entry in entries[:5]:
            print(f"   - {entry['type']}: ${entry['amount']}")
    else:
        print("\n   No ledger entries yet")

    # Summary
    print("\n" + "=" * 60)
    print("Complete Flow Summary")
    print("=" * 60)
    print(f"""
┌─────────────────────────────────────────────────────────┐
│  1. Platform provides deposit address                   │
│  2. Agent sends USDC to platform (auto-credited)        │
│  3. Agent creates session key with spending limits      │
│  4. Agent spends via signed transactions               │
│  5. Platform executes on-chain, debits balance         │
│  6. Agent can withdraw remaining balance anytime       │
└─────────────────────────────────────────────────────────┘

This is the Alancoin business model:
- Agents deposit funds to the platform
- Platform provides bounded autonomy via session keys
- Platform takes a fee on deposits/withdrawals (future)
- Network effects from agent registry + reputation
""")


if __name__ == "__main__":
    main()
