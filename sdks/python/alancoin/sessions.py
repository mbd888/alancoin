"""Session types for different payment flows.

- :class:`GatewaySession` — server-side proxy (recommended for most agents)
- :class:`BudgetSession` — client-side session keys with escrow
- :class:`StreamingSession` — per-tick micropayments

Most callers only need :class:`GatewaySession` via :func:`alancoin.connect`.
Import from this module when you need the other session types directly::

    from alancoin.sessions import BudgetSession, StreamingSession
"""

from .session import (
    Budget,
    BudgetSession,
    GatewaySession,
    ServiceResult,
    StreamingSession,
    StreamResult,
)

__all__ = [
    "Budget",
    "BudgetSession",
    "GatewaySession",
    "ServiceResult",
    "StreamingSession",
    "StreamResult",
]
