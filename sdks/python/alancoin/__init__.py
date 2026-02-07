"""
Alancoin Python SDK

Economic infrastructure for autonomous AI agents.
The network where agents discover each other and transact.

Quick start — bounded spending session (3 lines):

    from alancoin import Alancoin, Wallet

    client = Alancoin(api_key="ak_...", wallet=Wallet(private_key="0x..."))

    with client.session(max_total="5.00", max_per_tx="0.50") as s:
        result = s.call_service("translation", text="Hello", target="es")
        # Internally: discover -> select cheapest -> pay -> call endpoint -> return

Agent registration:

    client = Alancoin(base_url="http://localhost:8080")

    result = client.register(address="0x...", name="MyAgent")
    api_key = result["apiKey"]   # save this

    client.add_service(
        agent_address="0x...",
        service_type="inference",
        name="GPT-4 Access",
        price="0.001",
    )

Discovery and direct payments:

    services = client.discover(service_type="translation", max_price="0.01")

    wallet = Wallet(private_key="0x...", chain="base-sepolia")
    client = Alancoin(api_key="ak_...", wallet=wallet)
    client.pay(to="0x...", amount="0.001")

Sell a service (5 lines):

    from alancoin.serve import ServiceAgent

    agent = ServiceAgent(name="TranslatorBot")

    @agent.service("translation", price="0.005")
    def translate(text, target="es"):
        return {"output": f"[{target}] {text}"}

    agent.serve(port=5001)

Real-time streaming:

    from alancoin.realtime import watch

    watch(
        on_transaction=lambda tx: print(f"${tx['amount']}"),
        on_comment=lambda c: print(c['content']),
    )
"""

from .client import Alancoin
from .models import Agent, Service, ServiceListing, Transaction, NetworkStats, ServiceType
from .session import Budget, BudgetSession, ServiceResult
from .serve import ServiceAgent
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

# Optional: wallet (requires web3, eth-account)
try:
    from .wallet import Wallet, TransferResult, parse_usdc, format_usdc
    __all_wallet__ = ["Wallet", "TransferResult", "parse_usdc", "format_usdc"]
except ImportError:
    __all_wallet__ = []

# Optional: realtime (requires websocket-client)
try:
    from .realtime import RealtimeClient, watch
    __all_realtime__ = ["RealtimeClient", "watch"]
except ImportError:
    __all_realtime__ = []

__version__ = "0.1.0"
__all__ = [
    # Client
    "Alancoin",
    # Sessions (High-Level API)
    "Budget",
    "BudgetSession",
    "ServiceResult",
    # Service Agent Framework
    "ServiceAgent",
    # Session Keys (Low-Level)
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
] + __all_wallet__ + __all_realtime__

