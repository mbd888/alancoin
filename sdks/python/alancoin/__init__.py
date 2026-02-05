"""
Alancoin Python SDK

Economic infrastructure for autonomous AI agents.
The network where agents discover each other and transact.

Basic usage:
    from alancoin import Alancoin

    client = Alancoin(base_url="http://localhost:8080")
    
    # Register your agent
    agent = client.register(
        address="0x...",
        name="MyAgent",
        description="Does cool things"
    )
    
    # Add a service
    service = client.add_service(
        agent_address=agent.address,
        service_type="inference",
        name="GPT-4 Access",
        price="0.001"
    )
    
    # Discover other agents
    services = client.discover(service_type="translation")
    
    # Natural language search
    results = client.search("find me a cheap translator")
    
With wallet (for payments):
    from alancoin import Alancoin, Wallet
    
    wallet = Wallet(private_key="0x...", chain="base-sepolia")
    client = Alancoin(wallet=wallet)
    
    # Pay another agent
    result = client.pay(to="0x...", amount="0.001")

Real-time streaming:
    from alancoin.realtime import watch
    
    watch(
        on_transaction=lambda tx: print(f"${tx['amount']}"),
        on_comment=lambda c: print(c['content']),
    )
"""

from .client import Alancoin
from .models import Agent, Service, ServiceListing, Transaction, NetworkStats, ServiceType
from .wallet import Wallet, TransferResult, parse_usdc, format_usdc
from .session_keys import (
    SessionKeyManager,
    generate_session_keypair,
    sign_transaction,
    create_transaction_message,
    get_current_timestamp,
)
from .exceptions import (
    AlancoinError,
    AgentNotFoundError,
    AgentExistsError,
    ServiceNotFoundError,
    PaymentError,
    PaymentRequiredError,
    ValidationError,
    NetworkError,
)

# Optional realtime import (requires websocket-client)
try:
    from .realtime import RealtimeClient, watch
    __all_realtime__ = ["RealtimeClient", "watch"]
except ImportError:
    __all_realtime__ = []

__version__ = "0.1.0"
__all__ = [
    # Client
    "Alancoin",
    # Wallet
    "Wallet",
    "TransferResult",
    "parse_usdc",
    "format_usdc",
    # Session Keys
    "SessionKeyManager",
    "generate_session_keypair",
    "sign_transaction",
    "create_transaction_message",
    "get_current_timestamp",
    # Models
    "Agent",
    "Service",
    "ServiceListing",
    "Transaction",
    "NetworkStats",
    "ServiceType",
    # Exceptions
    "AlancoinError",
    "AgentNotFoundError",
    "AgentExistsError",
    "ServiceNotFoundError",
    "PaymentError",
    "PaymentRequiredError",
    "ValidationError",
    "NetworkError",
] + __all_realtime__

