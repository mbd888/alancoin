"""
Session key cryptography helpers for Alancoin.

Session keys are ECDSA keypairs that prove the agent controls the key
when making transactions. The workflow:

1. Generate a keypair: private_key, public_key = generate_session_keypair()
2. Register with server: client.create_session_key(..., public_key=public_key)
3. Sign transactions: signature = sign_transaction(to, amount, nonce, timestamp, private_key)
4. Submit: client.transact_with_session_key(..., signature=signature)
"""

import time
from typing import Tuple

try:
    from eth_account import Account
    from eth_account.messages import encode_defunct
    HAS_ETH_ACCOUNT = True
except ImportError:
    HAS_ETH_ACCOUNT = False


def generate_session_keypair() -> Tuple[str, str]:
    """
    Generate a new ECDSA keypair for session key authentication.
    
    Returns:
        Tuple of (private_key_hex, public_address)
        - private_key_hex: The private key as hex string (keep secret!)
        - public_address: The Ethereum address (register this with the server)
    
    Example:
        private_key, public_key = generate_session_keypair()
        
        # Register the public key with the server
        key = client.create_session_key(
            agent_address=wallet.address,
            public_key=public_key,  # The address
            max_per_day="10.00",
        )
        
        # Store the private key securely
        # You'll need it to sign transactions
    """
    if not HAS_ETH_ACCOUNT:
        raise ImportError(
            "eth_account is required for cryptographic session keys. "
            "Install with: pip install eth-account"
        )
    
    account = Account.create()
    private_key = account.key.hex()
    public_address = account.address.lower()
    
    return private_key, public_address


def create_transaction_message(to: str, amount: str, nonce: int, timestamp: int) -> str:
    """
    Create the message that must be signed for a transaction.
    
    Format: "Alancoin|{to}|{amount}|{nonce}|{timestamp}"
    
    This must match the server's expected format exactly.
    
    Args:
        to: Recipient address (will be lowercased)
        amount: USDC amount as string
        nonce: Unique number per transaction (increment each time)
        timestamp: Unix timestamp in seconds
    
    Returns:
        Message string to sign
    """
    return f"Alancoin|{to.lower()}|{amount}|{nonce}|{timestamp}"


def sign_transaction(
    to: str,
    amount: str,
    nonce: int,
    timestamp: int,
    private_key: str,
) -> str:
    """
    Sign a transaction with a session key's private key.
    
    Args:
        to: Recipient address
        amount: USDC amount as string
        nonce: Unique number (must be greater than last used nonce)
        timestamp: Unix timestamp (must be within 5 minutes of server time)
        private_key: The session key's private key (hex string)
    
    Returns:
        Signature as hex string (with 0x prefix)
    
    Example:
        signature = sign_transaction(
            to="0x1234...",
            amount="0.50",
            nonce=1,
            timestamp=int(time.time()),
            private_key=session_private_key,
        )
    """
    if not HAS_ETH_ACCOUNT:
        raise ImportError(
            "eth_account is required for signing. "
            "Install with: pip install eth-account"
        )
    
    # Create the message
    message = create_transaction_message(to, amount, nonce, timestamp)
    
    # Sign with Ethereum prefix (EIP-191)
    message_encoded = encode_defunct(text=message)
    signed = Account.sign_message(message_encoded, private_key=private_key)
    
    return signed.signature.hex()


def get_current_timestamp() -> int:
    """Get current Unix timestamp in seconds."""
    return int(time.time())


class SessionKeyManager:
    """
    Helper class for managing a session key's cryptographic operations.
    
    Example:
        # Create a session key manager
        skm = SessionKeyManager()
        
        # Register the public key
        key = client.create_session_key(
            agent_address=wallet.address,
            public_key=skm.public_key,
            max_per_day="10.00",
        )
        skm.set_key_id(key["id"])
        
        # Sign and submit a transaction
        result = skm.transact(client, wallet.address, "0x...", "0.50")
    """
    
    def __init__(self, private_key: str = None):
        """
        Initialize the session key manager.
        
        Args:
            private_key: Existing private key, or None to generate new
        """
        if private_key:
            if not HAS_ETH_ACCOUNT:
                raise ImportError("eth_account required")
            self.private_key = private_key
            account = Account.from_key(private_key)
            self.public_key = account.address.lower()
        else:
            self.private_key, self.public_key = generate_session_keypair()
        
        self.key_id = None
        self._nonce = 0
    
    def set_key_id(self, key_id: str):
        """Set the session key ID after registration."""
        self.key_id = key_id
    
    @property
    def next_nonce(self) -> int:
        """Get and increment the nonce."""
        self._nonce += 1
        return self._nonce
    
    def sign(self, to: str, amount: str) -> dict:
        """
        Sign a transaction and return all required parameters.
        
        Returns:
            Dict with to, amount, nonce, timestamp, signature
        """
        nonce = self.next_nonce
        timestamp = get_current_timestamp()
        signature = sign_transaction(to, amount, nonce, timestamp, self.private_key)
        
        return {
            "to": to,
            "amount": amount,
            "nonce": nonce,
            "timestamp": timestamp,
            "signature": signature,
        }
    
    def transact(self, client, agent_address: str, to: str, amount: str, service_id: str = None) -> dict:
        """
        Sign and submit a transaction using this session key.
        
        Args:
            client: Alancoin client instance
            agent_address: The agent's address
            to: Recipient address
            amount: USDC amount
            service_id: Optional service ID
            
        Returns:
            Transaction result from server
        """
        if not self.key_id:
            raise ValueError("key_id not set - register the session key first")
        
        signed = self.sign(to, amount)
        
        return client.transact_with_session_key(
            agent_address=agent_address,
            key_id=self.key_id,
            to=signed["to"],
            amount=signed["amount"],
            nonce=signed["nonce"],
            timestamp=signed["timestamp"],
            signature=signed["signature"],
            service_id=service_id,
        )
