"""
Wallet integration for Alancoin SDK.

Handles USDC transfers on Base Sepolia/Mainnet.
"""

import json
import time
from typing import Optional, Tuple
from dataclasses import dataclass

from eth_account import Account
from eth_account.signers.local import LocalAccount
from web3 import Web3
from web3.middleware import geth_poa_middleware

from .exceptions import PaymentError, ValidationError


# USDC has 6 decimals
USDC_DECIMALS = 6

# ERC20 minimal ABI
ERC20_ABI = json.loads("""[
    {"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},
    {"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"}
]""")

# Chain configurations
CHAINS = {
    "base-sepolia": {
        "chain_id": 84532,
        "rpc_url": "https://sepolia.base.org",
        "usdc_contract": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    },
    "base": {
        "chain_id": 8453,
        "rpc_url": "https://mainnet.base.org",
        "usdc_contract": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
    },
}


@dataclass
class TransferResult:
    """Result of a USDC transfer."""
    
    tx_hash: str
    from_address: str
    to_address: str
    amount: str  # Human-readable USDC
    block_number: Optional[int] = None
    gas_used: Optional[int] = None


class Wallet:
    """
    Wallet for USDC transfers.
    
    Example:
        wallet = Wallet(
            private_key="0x...",
            chain="base-sepolia"
        )
        
        # Check balance
        balance = wallet.balance()
        print(f"Balance: {balance} USDC")
        
        # Send payment
        result = wallet.transfer("0xRecipient", "0.001")
        print(f"TX: {result.tx_hash}")
    """
    
    def __init__(
        self,
        private_key: str,
        chain: str = "base-sepolia",
        rpc_url: str = None,
    ):
        """
        Initialize wallet.
        
        Args:
            private_key: Private key (hex string, with or without 0x)
            chain: Chain name ("base-sepolia" or "base")
            rpc_url: Optional custom RPC URL
        """
        if chain not in CHAINS:
            raise ValidationError(f"Unknown chain: {chain}. Use 'base-sepolia' or 'base'")
        
        self.chain = chain
        self.chain_config = CHAINS[chain]
        
        # Connect to RPC
        rpc = rpc_url or self.chain_config["rpc_url"]
        self.w3 = Web3(Web3.HTTPProvider(rpc, request_kwargs={"timeout": 30}))
        self.w3.middleware_onion.inject(geth_poa_middleware, layer=0)
        
        # Load account
        if not private_key.startswith("0x"):
            private_key = "0x" + private_key
        self.account: LocalAccount = Account.from_key(private_key)
        
        # USDC contract
        self.usdc = self.w3.eth.contract(
            address=Web3.to_checksum_address(self.chain_config["usdc_contract"]),
            abi=ERC20_ABI,
        )
    
    @property
    def address(self) -> str:
        """Get wallet address."""
        return self.account.address
    
    def balance(self, address: str = None) -> str:
        """
        Get USDC balance.
        
        Args:
            address: Address to check (defaults to own wallet)
            
        Returns:
            Balance as human-readable string (e.g., "1.50")
        """
        addr = address or self.address
        raw = self.usdc.functions.balanceOf(
            Web3.to_checksum_address(addr)
        ).call()
        return format_usdc(raw)
    
    def transfer(
        self,
        to: str,
        amount: str,
        wait_for_confirmation: bool = True,
        timeout: int = 60,
    ) -> TransferResult:
        """
        Send USDC to another address.
        
        Args:
            to: Recipient address
            amount: Amount in USDC (e.g., "0.001")
            wait_for_confirmation: Wait for tx to be mined
            timeout: Max seconds to wait for confirmation
            
        Returns:
            TransferResult with tx hash and details
            
        Raises:
            PaymentError: If transfer fails
        """
        to_addr = Web3.to_checksum_address(to)
        amount_raw = parse_usdc(amount)
        
        # Build transaction
        try:
            nonce = self.w3.eth.get_transaction_count(self.address)
            gas_price = self.w3.eth.gas_price
            
            tx = self.usdc.functions.transfer(
                to_addr,
                amount_raw,
            ).build_transaction({
                "from": self.address,
                "nonce": nonce,
                "gasPrice": gas_price,
                "chainId": self.chain_config["chain_id"],
            })
            
            # Estimate gas
            try:
                tx["gas"] = self.w3.eth.estimate_gas(tx)
            except Exception:
                tx["gas"] = 100000  # Default for ERC20 transfers
            
            # Sign and send
            signed = self.account.sign_transaction(tx)
            tx_hash = self.w3.eth.send_raw_transaction(signed.raw_transaction)
            tx_hash_hex = tx_hash.hex()
            
        except Exception as e:
            raise PaymentError(f"Transfer failed: {e}")
        
        result = TransferResult(
            tx_hash=tx_hash_hex,
            from_address=self.address,
            to_address=to,
            amount=amount,
        )
        
        # Wait for confirmation
        if wait_for_confirmation:
            try:
                receipt = self._wait_for_receipt(tx_hash_hex, timeout)
                if receipt["status"] == 0:
                    raise PaymentError(
                        f"Transaction reverted",
                        tx_hash=tx_hash_hex,
                    )
                result.block_number = receipt["blockNumber"]
                result.gas_used = receipt["gasUsed"]
            except PaymentError:
                raise
            except Exception as e:
                raise PaymentError(
                    f"Failed waiting for confirmation: {e}",
                    tx_hash=tx_hash_hex,
                )
        
        return result
    
    def _wait_for_receipt(self, tx_hash: str, timeout: int) -> dict:
        """Wait for transaction receipt."""
        start = time.time()
        while time.time() - start < timeout:
            try:
                receipt = self.w3.eth.get_transaction_receipt(tx_hash)
                if receipt:
                    return receipt
            except Exception:
                pass
            time.sleep(2)
        raise PaymentError(f"Transaction not confirmed within {timeout}s", tx_hash=tx_hash)


def parse_usdc(amount: str) -> int:
    """
    Parse human-readable USDC amount to smallest units.
    
    Args:
        amount: Human-readable amount (e.g., "1.50")
        
    Returns:
        Amount in smallest units (e.g., 1500000)
    """
    if not amount:
        raise ValidationError("Amount cannot be empty")

    if amount.startswith("-"):
        raise ValidationError("Amount cannot be negative")

    try:
        # Handle decimal
        if "." in amount:
            parts = amount.split(".")
            if len(parts) != 2:
                raise ValueError("Invalid decimal format")
            integer_part = int(parts[0]) if parts[0] else 0
            decimal_part = parts[1][:USDC_DECIMALS].ljust(USDC_DECIMALS, "0")
            return integer_part * (10 ** USDC_DECIMALS) + int(decimal_part)
        else:
            return int(amount) * (10 ** USDC_DECIMALS)
    except ValueError as e:
        raise ValidationError(f"Invalid amount: {amount}")


def format_usdc(amount: int) -> str:
    """
    Format smallest units to human-readable USDC.
    
    Args:
        amount: Amount in smallest units
        
    Returns:
        Human-readable amount (e.g., "1.500000")
    """
    if amount is None or amount == 0:
        return "0"
    
    integer_part = amount // (10 ** USDC_DECIMALS)
    decimal_part = amount % (10 ** USDC_DECIMALS)
    
    if decimal_part == 0:
        return str(integer_part)
    
    decimal_str = str(decimal_part).zfill(USDC_DECIMALS)
    return f"{integer_part}.{decimal_str}"
