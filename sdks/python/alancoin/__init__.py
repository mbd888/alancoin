"""Alancoin Python SDK — Agent Financial Controller.

The ``with`` block is the entire product::

    from alancoin import connect

    with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
        result = gw.call("translation", text="Hello", target="es")
        print(result["output"])

One-shot variant::

    from alancoin import spend

    result = spend("http://localhost:8080", api_key="ak_...",
                   service_type="translation", budget="1.00",
                   text="Hello", target="es")
"""

from ._connect import connect, spend
from .exceptions import AlancoinError, PolicyDeniedError
from .session import Budget, GatewaySession

__version__ = "0.2.0"
__all__ = [
    "connect",
    "spend",
    "Budget",
    "GatewaySession",
    "AlancoinError",
    "PolicyDeniedError",
]

# ---------------------------------------------------------------------------
# Backward compatibility — legacy top-level imports emit DeprecationWarning
# ---------------------------------------------------------------------------

import warnings as _warnings

_LEGACY_MOVES = {
    # client
    "Alancoin": ("alancoin.admin", "Alancoin"),
    # session types
    "BudgetSession": ("alancoin.sessions", "BudgetSession"),
    "ServiceResult": ("alancoin.sessions", "ServiceResult"),
    "StreamingSession": ("alancoin.sessions", "StreamingSession"),
    "StreamResult": ("alancoin.sessions", "StreamResult"),
    # models
    "Agent": ("alancoin.models", "Agent"),
    "Service": ("alancoin.models", "Service"),
    "ServiceListing": ("alancoin.models", "ServiceListing"),
    "Transaction": ("alancoin.models", "Transaction"),
    "NetworkStats": ("alancoin.models", "NetworkStats"),
    "ServiceType": ("alancoin.models", "ServiceType"),
    # session keys
    "SessionKeyManager": ("alancoin.session_keys", "SessionKeyManager"),
    "generate_session_keypair": ("alancoin.session_keys", "generate_session_keypair"),
    "sign_transaction": ("alancoin.session_keys", "sign_transaction"),
    "sign_delegation": ("alancoin.session_keys", "sign_delegation"),
    "create_transaction_message": ("alancoin.session_keys", "create_transaction_message"),
    "create_delegation_message": ("alancoin.session_keys", "create_delegation_message"),
    "get_current_timestamp": ("alancoin.session_keys", "get_current_timestamp"),
    # exceptions
    "AgentNotFoundError": ("alancoin.exceptions", "AgentNotFoundError"),
    "AgentExistsError": ("alancoin.exceptions", "AgentExistsError"),
    "ServiceNotFoundError": ("alancoin.exceptions", "ServiceNotFoundError"),
    "PaymentError": ("alancoin.exceptions", "PaymentError"),
    "PaymentRequiredError": ("alancoin.exceptions", "PaymentRequiredError"),
    "ValidationError": ("alancoin.exceptions", "ValidationError"),
    "NetworkError": ("alancoin.exceptions", "NetworkError"),
    # MCP proxy
    "MCPPaymentProxy": ("alancoin.mcp_proxy", "MCPPaymentProxy"),
    "ToolPricing": ("alancoin.mcp_proxy", "ToolPricing"),
    "DemoBackend": ("alancoin.mcp_proxy", "DemoBackend"),
    "AlancoinBackend": ("alancoin.mcp_proxy", "AlancoinBackend"),
}


def __getattr__(name: str):
    if name in _LEGACY_MOVES:
        new_module, attr = _LEGACY_MOVES[name]
        _warnings.warn(
            f"Importing {name!r} from 'alancoin' is deprecated. "
            f"Use 'from {new_module} import {attr}' instead.",
            DeprecationWarning,
            stacklevel=2,
        )
        import importlib
        mod = importlib.import_module(new_module)
        return getattr(mod, attr)
    raise AttributeError(f"module 'alancoin' has no attribute {name!r}")
