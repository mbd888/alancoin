"""
Alancoin Python SDK

Economic infrastructure for autonomous AI agents.
The network where agents discover each other and transact.

Quick start -- gateway session (3 lines):

    from alancoin import Alancoin

    client = Alancoin("http://localhost:8080", api_key="ak_...")

    with client.gateway(max_total="5.00") as gw:
        result = gw.call("translation", text="Hello", target="es")
        # Server discovers cheapest translator, pays, forwards, returns result

Sell a service (5 lines):

    from alancoin.serve import ServiceAgent

    agent = ServiceAgent(name="TranslatorBot")

    @agent.service("translation", price="0.005")
    def translate(text, target="es"):
        return {"output": f"[{target}] {text}"}

    agent.serve(port=5001)

Advanced (client-side session keys):

    with client.session(max_total="5.00", max_per_tx="0.50") as s:
        result = s.call_service("translation", text="Hello", target="es")
"""

from .client import Alancoin
from .models import (
    Agent, Service, ServiceListing, Transaction, NetworkStats, ServiceType,
)
from .session import Budget, BudgetSession, ServiceResult, StreamingSession, StreamResult, GatewaySession
from .serve import ServiceAgent, DelegationContext
from .session_keys import (
    SessionKeyManager,
    generate_session_keypair,
    sign_transaction,
    sign_delegation,
    create_transaction_message,
    create_delegation_message,
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

# Optional: MCP payment proxy (requires mcp)
try:
    from .mcp_proxy import MCPPaymentProxy, ToolPricing, DemoBackend, AlancoinBackend
    __all_mcp__ = ["MCPPaymentProxy", "ToolPricing", "DemoBackend", "AlancoinBackend"]
except ImportError:
    __all_mcp__ = []

__version__ = "0.1.0"
__all__ = [
    # Client
    "Alancoin",
    # Sessions (High-Level API)
    "Budget",
    "BudgetSession",
    "ServiceResult",
    # Streaming Micropayments
    "StreamingSession",
    "StreamResult",
    # Gateway (Transparent Payment Proxy)
    "GatewaySession",
    # Service Agent Framework
    "ServiceAgent",
    "DelegationContext",
    # Session Keys (Low-Level)
    "SessionKeyManager",
    "generate_session_keypair",
    "sign_transaction",
    "sign_delegation",
    "create_transaction_message",
    "create_delegation_message",
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
] + __all_wallet__ + __all_realtime__ + __all_mcp__

