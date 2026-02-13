"""Convenience entry points for the Alancoin SDK.

``connect()`` and ``spend()`` are the primary public API.  They compose
an :class:`~alancoin.client.Alancoin` client and a
:class:`~alancoin.session.GatewaySession` so the caller never touches
either directly.
"""

from contextlib import contextmanager
from typing import List, Optional


@contextmanager
def connect(
    url: str,
    api_key: str,
    budget: str,
    max_per_call: str = None,
    expires_in: str = "1h",
    allowed_services: Optional[List[str]] = None,
    timeout: int = 30,
):
    """Open a gateway session and yield it as a context manager.

    This is the recommended way to interact with the Alancoin platform::

        from alancoin import connect

        with connect("http://localhost:8080", api_key="ak_...", budget="5.00") as gw:
            result = gw.call("translation", text="Hello", target="es")
            print(result["output"])

    Args:
        url: Alancoin platform URL.
        api_key: API key for the calling agent.
        budget: Maximum USDC to hold for this session (e.g. ``"5.00"``).
        max_per_call: Maximum USDC per proxy request (defaults to *budget*).
        expires_in: Session duration (e.g. ``"1h"``, ``"24h"``).
        allowed_services: Restrict to these service types.
        timeout: HTTP request timeout in seconds.

    Yields:
        :class:`~alancoin.session.GatewaySession`
    """
    from .client import Alancoin

    client = Alancoin(base_url=url, api_key=api_key, timeout=timeout)
    try:
        with client.gateway(
            max_total=budget,
            max_per_tx=max_per_call,
            expires_in=expires_in,
            allowed_services=allowed_services,
        ) as gw:
            yield gw
    finally:
        client.close()


def spend(
    url: str,
    api_key: str,
    service_type: str,
    budget: str,
    timeout: int = 30,
    idempotency_key: str = None,
    **params,
) -> dict:
    """One-shot: open a session, call a service, close, return the result.

    Useful when you need exactly one service call::

        from alancoin import spend

        result = spend(
            "http://localhost:8080",
            api_key="ak_...",
            service_type="translation",
            budget="1.00",
            text="Hello",
            target="es",
        )
        print(result["output"])

    Args:
        url: Alancoin platform URL.
        api_key: API key for the calling agent.
        service_type: Type of service to call (e.g. ``"translation"``).
        budget: Maximum USDC to hold (only what's needed is charged).
        timeout: HTTP request timeout in seconds.
        idempotency_key: Client-provided key to prevent double-charges on retry.
        **params: Forwarded to the service endpoint.

    Returns:
        Service response dict (the ``result["output"]`` field contains the
        service's primary output).
    """
    with connect(url, api_key=api_key, budget=budget, timeout=timeout) as gw:
        return gw.call(service_type, idempotency_key=idempotency_key, **params)
