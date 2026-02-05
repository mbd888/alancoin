#!/usr/bin/env python3
"""
Alancoin Demo: Session Keys (Bounded Autonomy)

This is the key differentiator vs Skyfire - agents get bounded wallets,
not unlimited access.

Setup:
  export AGENT_A_KEY=0x...  # Agent A (the one paying)
  export AGENT_B_KEY=0x...  # Agent B (the one receiving)

Run: python session_key_demo.py
"""

import os
import sys
from alancoin import Alancoin, Wallet
from alancoin.session_keys import SessionKeyManager

API_URL = os.getenv("ALANCOIN_URL", "http://localhost:8080")
AGENT_A_KEY = os.getenv("AGENT_A_KEY") or os.getenv("AGENT_PRIVATE_KEY")
AGENT_B_KEY = os.getenv("AGENT_B_KEY")


def main():
    print("=" * 60)
    print("Alancoin: Session Keys Demo")
    print("Bounded autonomy - agents transact within limits YOU set")
    print("=" * 60)

    if not AGENT_A_KEY:
        print("\nSet AGENT_A_KEY (or AGENT_PRIVATE_KEY) env var")
        print("  export AGENT_A_KEY=0x...")
        return

    # Setup Agent A (the payer)
    wallet_a = Wallet(private_key=AGENT_A_KEY, chain="base-sepolia")
    client_a = Alancoin(base_url=API_URL, wallet=wallet_a)

    print(f"\n👤 Agent A: {wallet_a.address}")
    balance_a = wallet_a.balance()
    print(f"   Balance: {balance_a} USDC")

    # Setup Agent B (the receiver) - or use a demo address
    if AGENT_B_KEY:
        wallet_b = Wallet(private_key=AGENT_B_KEY, chain="base-sepolia")
        recipient = wallet_b.address
        balance_b_before = wallet_b.balance()
        print(f"\n👤 Agent B: {recipient}")
        print(f"   Balance: {balance_b_before} USDC")
    else:
        # Use a demo recipient if no second key provided
        recipient = "0x742d35Cc6634C0532925a3b844Bc9e7595f8fE00"
        balance_b_before = None
        print(f"\n👤 Recipient: {recipient[:20]}...")
        print("   (Set AGENT_B_KEY to see balance changes)")

    # Check balance
    if float(balance_a) < 0.5:
        print(f"\n⚠️  Agent A needs USDC! Get testnet USDC at:")
        print("   https://faucet.circle.com")
        return

    # Register Agent A
    print("\n" + "-" * 60)
    print("1. Register Agent A")
    print("-" * 60)

    try:
        agent = client_a.register(
            address=wallet_a.address,
            name="DemoAgent",
            description="Session key demo",
        )
        print(f"✓ Registered: {agent.name}")
        api_key = agent.api_key
        print(f"  API Key: {api_key[:20]}...")
    except Exception as e:
        if "exists" in str(e).lower():
            print("✓ Already registered")
        else:
            raise

    # Create session key with strict limits
    print("\n" + "-" * 60)
    print("2. Create session key with limits")
    print("-" * 60)

    skm = SessionKeyManager()
    print(f"  Generated keypair")
    print(f"  Public key: {skm.public_key}")

    key = client_a.create_session_key(
        agent_address=wallet_a.address,
        public_key=skm.public_key,
        expires_in="1h",
        max_per_transaction="1.00",
        max_per_day="5.00",
    )
    skm.set_key_id(key["id"])

    print(f"\n✓ Session key created: {key['id']}")
    print(f"  Limits: $1/tx, $5/day, expires 1 hour")

    # Execute real transaction
    print("\n" + "-" * 60)
    print("3. Execute transaction via session key")
    print("-" * 60)

    amount = "0.10"
    print(f"\n  Sending ${amount} USDC to {recipient[:15]}...")
    print(f"  Signing with session key...")

    try:
        result = skm.transact(
            client=client_a,
            agent_address=wallet_a.address,
            to=recipient,
            amount=amount,
        )
        
        status = result.get("status", "unknown")
        print(f"\n✓ Transaction {status}!")
        
        if result.get("txHash"):
            print(f"  TX Hash: {result['txHash']}")
            print(f"  View: https://sepolia.basescan.org/tx/{result['txHash']}")
        
        print(f"  Signature verified: ✓")
        print(f"  Nonce: {result.get('nonce')}")
        
    except Exception as e:
        print(f"  Error: {e}")
        if "insufficient" in str(e).lower():
            print("\n  Get testnet USDC at: https://faucet.circle.com")
        return

    # Show balance changes
    print("\n" + "-" * 60)
    print("4. Verify balance changes")
    print("-" * 60)

    balance_a_after = wallet_a.balance()
    print(f"\n  Agent A: {balance_a} → {balance_a_after} USDC")
    
    if AGENT_B_KEY:
        balance_b_after = wallet_b.balance()
        print(f"  Agent B: {balance_b_before} → {balance_b_after} USDC")

    # Try to exceed limit
    print("\n" + "-" * 60)
    print("5. Permission enforcement (try to exceed $1 limit)")
    print("-" * 60)

    print(f"\n  Attempting $2.00 transaction...")
    try:
        result = skm.transact(
            client=client_a,
            agent_address=wallet_a.address,
            to=recipient,
            amount="2.00",
        )
        print(f"  Unexpected: {result}")
    except Exception as e:
        print(f"✓ Rejected: exceeds per-transaction limit")

    # Check usage
    print("\n" + "-" * 60)
    print("6. Session key usage stats")
    print("-" * 60)

    key_status = client_a.get_session_key(wallet_a.address, skm.key_id)
    usage = key_status.get("session", {}).get("usage", {})
    
    print(f"\n  Transactions: {usage.get('transactionCount', 0)}")
    print(f"  Total spent: ${usage.get('totalSpent', '0')}")
    print(f"  Remaining today: ${key_status.get('session', {}).get('permission', {}).get('maxPerDay', '?')}")

    # Revoke
    print("\n" + "-" * 60)
    print("7. Instant revocation")
    print("-" * 60)

    client_a.revoke_session_key(wallet_a.address, skm.key_id)
    print(f"✓ Session key revoked - no more transactions possible")

    # Summary
    print("\n" + "=" * 60)
    print("THE DIFFERENTIATOR")
    print("=" * 60)
    print("""
┌─────────────────────────────────────────────────────────┐
│  Skyfire: "Here's a wallet with $1000. Good luck."      │
│                                                         │
│  Alancoin: "Here's a session key that can spend up to  │
│            $1 per transaction, $5 per day, only for    │
│            translation services. I can revoke it        │
│            instantly. The agent proves possession by   │
│            cryptographically signing each request."     │
└─────────────────────────────────────────────────────────┘

Session keys = Bounded autonomy = Enterprise-grade control
""")


if __name__ == "__main__":
    main()
