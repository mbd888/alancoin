"""Real-time WebSocket event streaming for Alancoin.

Provides an async client that connects to the Alancoin WebSocket endpoint
and delivers events via callbacks. Supports subscription filtering,
auto-reconnection with exponential backoff, and context manager usage.

Requires the ``websockets`` library::

    pip install alancoin[realtime]

Usage::

    import asyncio
    from alancoin import Alancoin
    from alancoin.realtime import EventType, RealtimeSubscription

    client = Alancoin("http://localhost:8080", api_key="ak_...")

    async def main():
        async with client.realtime(
            on_event=lambda e: print(e.type, e.data),
            subscription=RealtimeSubscription(
                event_types=[EventType.TRANSACTION, EventType.ESCROW_CREATED],
            ),
        ) as rt:
            await rt.wait_closed()

    asyncio.run(main())
"""

from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass, field
from typing import Callable, Dict, List, Optional, Any
from urllib.parse import quote

try:
    import websockets
    import websockets.exceptions
except ImportError as _err:
    raise ImportError(
        "The 'websockets' package is required for real-time streaming. "
        "Install it with: pip install alancoin[realtime]"
    ) from _err

logger = logging.getLogger("alancoin.realtime")


# ---------------------------------------------------------------------------
# Event types — mirrors internal/realtime/hub.go constants
# ---------------------------------------------------------------------------


class EventType:
    """Real-time event type constants matching the server protocol."""

    TRANSACTION = "transaction"
    AGENT_JOINED = "agent_joined"
    MILESTONE = "milestone"
    PRICE_ALERT = "price_alert"
    COALITION = "coalition"

    SESSION_CREATED = "session_created"
    SESSION_CLOSED = "session_closed"
    PROXY_SETTLEMENT = "proxy_settlement"

    ESCROW_CREATED = "escrow_created"
    ESCROW_DELIVERED = "escrow_delivered"
    ESCROW_CONFIRMED = "escrow_confirmed"
    ESCROW_DISPUTED = "escrow_disputed"

    STREAM_OPENED = "stream_opened"
    STREAM_CLOSED = "stream_closed"

    ALL: List[str] = [
        TRANSACTION, AGENT_JOINED, MILESTONE, PRICE_ALERT, COALITION,
        SESSION_CREATED, SESSION_CLOSED, PROXY_SETTLEMENT,
        ESCROW_CREATED, ESCROW_DELIVERED, ESCROW_CONFIRMED, ESCROW_DISPUTED,
        STREAM_OPENED, STREAM_CLOSED,
    ]


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------


@dataclass
class RealtimeEvent:
    """A real-time event received from the WebSocket stream."""

    type: str
    timestamp: str
    data: Dict[str, Any]

    @classmethod
    def from_dict(cls, raw: dict) -> RealtimeEvent:
        return cls(
            type=raw.get("type", ""),
            timestamp=raw.get("timestamp", ""),
            data=raw.get("data", {}),
        )


@dataclass
class RealtimeSubscription:
    """Subscription filter controlling which events are delivered.

    A default subscription (``all_events=True``) receives everything.
    Set specific filters to narrow the stream.
    """

    all_events: bool = True
    event_types: Optional[List[str]] = None
    agent_addrs: Optional[List[str]] = None
    service_types: Optional[List[str]] = None
    min_amount: Optional[float] = None

    def to_dict(self) -> dict:
        """Serialize to the JSON wire format matching the server protocol."""
        d: Dict[str, Any] = {"allEvents": self.all_events}
        if self.event_types:
            d["eventTypes"] = self.event_types
        if self.agent_addrs:
            d["agentAddrs"] = self.agent_addrs
        if self.service_types:
            d["serviceTypes"] = self.service_types
        if self.min_amount is not None:
            d["minAmount"] = self.min_amount
        return d


@dataclass
class RealtimeStats:
    """Connection statistics."""

    events_received: int = 0
    reconnects: int = 0


# ---------------------------------------------------------------------------
# Callback types
# ---------------------------------------------------------------------------

EventHandler = Callable[[RealtimeEvent], None]
ErrorHandler = Callable[[Exception], bool]  # return True to reconnect


# ---------------------------------------------------------------------------
# Client
# ---------------------------------------------------------------------------


class RealtimeClient:
    """Async WebSocket client for real-time Alancoin events.

    Use as an async context manager::

        async with RealtimeClient(base_url, api_key, on_event=handler) as rt:
            await rt.wait_closed()
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        subscription: Optional[RealtimeSubscription] = None,
        on_event: Optional[EventHandler] = None,
        on_error: Optional[ErrorHandler] = None,
        reconnect_backoff: float = 1.0,
        reconnect_max: float = 30.0,
        ping_interval: float = 30.0,
    ) -> None:
        self._base_url = base_url
        self._api_key = api_key
        self._subscription = subscription or RealtimeSubscription()
        self._on_event = on_event
        self._on_error = on_error
        self._reconnect_backoff = reconnect_backoff
        self._reconnect_max = reconnect_max
        self._ping_interval = ping_interval

        self._ws: Optional[websockets.WebSocketClientProtocol] = None
        self._read_task: Optional[asyncio.Task[None]] = None
        self._closed = False
        self._events_received = 0
        self._reconnects = 0
        self._current_backoff = reconnect_backoff

    # -- Public API ----------------------------------------------------------

    async def start(self) -> None:
        """Start the background read loop. Called automatically by __aenter__."""
        if self._read_task is not None:
            return
        self._read_task = asyncio.create_task(self._read_loop())

    async def subscribe(self, subscription: RealtimeSubscription) -> None:
        """Update the subscription filter on a live connection."""
        self._subscription = subscription
        if self._ws is not None:
            try:
                await self._ws.send(json.dumps(subscription.to_dict()))
            except websockets.exceptions.ConnectionClosed:
                pass  # will re-send on reconnect

    async def close(self) -> None:
        """Shut down the connection and background task."""
        if self._closed:
            return
        self._closed = True
        if self._ws is not None:
            try:
                await self._ws.close()
            except Exception:
                pass
        if self._read_task is not None:
            self._read_task.cancel()
            try:
                await self._read_task
            except asyncio.CancelledError:
                pass

    async def wait_closed(self) -> None:
        """Block until the connection terminates (error or close)."""
        if self._read_task is not None:
            await self._read_task

    def stats(self) -> RealtimeStats:
        """Return connection statistics."""
        return RealtimeStats(
            events_received=self._events_received,
            reconnects=self._reconnects,
        )

    # -- Context manager -----------------------------------------------------

    async def __aenter__(self) -> RealtimeClient:
        await self.start()
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.close()

    # -- Internal ------------------------------------------------------------

    def _ws_url(self) -> str:
        u = self._base_url.rstrip("/")
        u = u.replace("https://", "wss://", 1).replace("http://", "ws://", 1)
        return f"{u}/ws?token={quote(self._api_key, safe='')}"

    async def _connect(self) -> websockets.WebSocketClientProtocol:
        url = self._ws_url()
        headers = {"X-API-Key": self._api_key}
        return await websockets.connect(
            url,
            additional_headers=headers,
            ping_interval=self._ping_interval,
            ping_timeout=self._ping_interval * 3,
            close_timeout=5,
        )

    async def _read_loop(self) -> None:
        while not self._closed:
            try:
                ws = await self._connect()
                self._ws = ws
                self._current_backoff = self._reconnect_backoff
                # Send initial subscription
                await ws.send(json.dumps(self._subscription.to_dict()))

                async for raw in ws:
                    if self._closed:
                        return
                    try:
                        data = json.loads(raw)
                        event = RealtimeEvent.from_dict(data)
                    except (json.JSONDecodeError, KeyError, TypeError):
                        continue  # skip malformed messages
                    self._events_received += 1
                    if self._on_event is not None:
                        try:
                            self._on_event(event)
                        except Exception:
                            logger.exception("on_event callback error")
                # Iterator exhausted — connection closed cleanly by server.
                # In production, websockets raises ConnectionClosed* exceptions
                # (caught below) rather than exhausting the iterator. A clean
                # iterator exit means the connection ended gracefully.
                return

            except asyncio.CancelledError:
                return
            except Exception as exc:
                if self._closed:
                    return
                logger.debug("WebSocket error: %s", exc)
                should_reconnect = True
                if self._on_error is not None:
                    try:
                        should_reconnect = self._on_error(exc)
                    except Exception:
                        logger.exception("on_error callback error")
                        should_reconnect = False
                if not should_reconnect:
                    return
                self._reconnects += 1
                await asyncio.sleep(self._current_backoff)
                self._current_backoff = min(
                    self._current_backoff * 2, self._reconnect_max
                )
