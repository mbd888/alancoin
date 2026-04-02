"""Tests for the real-time WebSocket event streaming client."""

import asyncio
import json
from unittest.mock import AsyncMock, patch

import pytest

from alancoin.realtime import (
    EventType,
    RealtimeClient,
    RealtimeEvent,
    RealtimeSubscription,
    RealtimeStats,
)


# ---------------------------------------------------------------------------
# Mock WebSocket
# ---------------------------------------------------------------------------


class MockWebSocket:
    """Simulates a websockets.WebSocketClientProtocol for testing."""

    def __init__(self, messages=None, raise_on_iter=None):
        self._messages = list(messages or [])
        self._raise_on_iter = raise_on_iter
        self.sent = []
        self.open = True

    async def send(self, data):
        self.sent.append(data)

    async def close(self):
        self.open = False

    def __aiter__(self):
        return self

    async def __anext__(self):
        if self._raise_on_iter:
            exc = self._raise_on_iter
            self._raise_on_iter = None
            raise exc
        if not self._messages:
            self.open = False
            raise StopAsyncIteration
        return self._messages.pop(0)


def make_event_json(event_type, data=None):
    return json.dumps({
        "type": event_type,
        "timestamp": "2026-03-24T12:00:00Z",
        "data": data or {},
    })


def run_async(coro):
    """Run an async coroutine synchronously, avoiding pytest-asyncio issues."""
    loop = asyncio.new_event_loop()
    try:
        return loop.run_until_complete(coro)
    finally:
        loop.close()


# ---------------------------------------------------------------------------
# EventType tests
# ---------------------------------------------------------------------------


class TestEventType:
    def test_all_types_defined(self):
        assert len(EventType.ALL) == 14

    def test_values_are_strings(self):
        for t in EventType.ALL:
            assert isinstance(t, str)

    def test_transaction_type(self):
        assert EventType.TRANSACTION == "transaction"

    def test_escrow_types(self):
        assert EventType.ESCROW_CREATED == "escrow_created"
        assert EventType.ESCROW_DELIVERED == "escrow_delivered"
        assert EventType.ESCROW_CONFIRMED == "escrow_confirmed"
        assert EventType.ESCROW_DISPUTED == "escrow_disputed"

    def test_session_types(self):
        assert EventType.SESSION_CREATED == "session_created"
        assert EventType.SESSION_CLOSED == "session_closed"
        assert EventType.PROXY_SETTLEMENT == "proxy_settlement"

    def test_stream_types(self):
        assert EventType.STREAM_OPENED == "stream_opened"
        assert EventType.STREAM_CLOSED == "stream_closed"


# ---------------------------------------------------------------------------
# RealtimeSubscription tests
# ---------------------------------------------------------------------------


class TestRealtimeSubscription:
    def test_default_all_events(self):
        sub = RealtimeSubscription()
        assert sub.all_events is True
        assert sub.to_dict() == {"allEvents": True}

    def test_to_dict_with_event_types(self):
        sub = RealtimeSubscription(
            all_events=False,
            event_types=[EventType.TRANSACTION, EventType.ESCROW_CREATED],
        )
        d = sub.to_dict()
        assert d["allEvents"] is False
        assert d["eventTypes"] == ["transaction", "escrow_created"]

    def test_to_dict_with_agent_filter(self):
        sub = RealtimeSubscription(agent_addrs=["0xabc", "0xdef"])
        d = sub.to_dict()
        assert d["agentAddrs"] == ["0xabc", "0xdef"]

    def test_to_dict_with_min_amount(self):
        sub = RealtimeSubscription(min_amount=10.5)
        d = sub.to_dict()
        assert d["minAmount"] == 10.5

    def test_to_dict_omits_none_filters(self):
        sub = RealtimeSubscription()
        d = sub.to_dict()
        assert "eventTypes" not in d
        assert "agentAddrs" not in d
        assert "serviceTypes" not in d
        assert "minAmount" not in d

    def test_to_dict_camel_case_keys(self):
        sub = RealtimeSubscription(
            all_events=True,
            event_types=["transaction"],
            agent_addrs=["0x1"],
            service_types=["translation"],
            min_amount=1.0,
        )
        d = sub.to_dict()
        assert "allEvents" in d
        assert "eventTypes" in d
        assert "agentAddrs" in d
        assert "serviceTypes" in d
        assert "minAmount" in d


# ---------------------------------------------------------------------------
# RealtimeEvent tests
# ---------------------------------------------------------------------------


class TestRealtimeEvent:
    def test_from_dict(self):
        raw = {
            "type": "transaction",
            "timestamp": "2026-03-24T12:00:00Z",
            "data": {"from": "0xA", "to": "0xB", "amount": "5.00"},
        }
        event = RealtimeEvent.from_dict(raw)
        assert event.type == "transaction"
        assert event.timestamp == "2026-03-24T12:00:00Z"
        assert event.data["from"] == "0xA"

    def test_from_dict_missing_fields(self):
        event = RealtimeEvent.from_dict({})
        assert event.type == ""
        assert event.timestamp == ""
        assert event.data == {}

    def test_from_dict_extra_fields(self):
        raw = {
            "type": "escrow_created",
            "timestamp": "2026-01-01T00:00:00Z",
            "data": {"escrowId": "esc_123"},
            "extra_field": "ignored",
        }
        event = RealtimeEvent.from_dict(raw)
        assert event.type == "escrow_created"


# ---------------------------------------------------------------------------
# RealtimeClient tests — using asyncio.run() to avoid pytest-asyncio issues
# ---------------------------------------------------------------------------


class TestRealtimeClient:
    def test_ws_url_http(self):
        rt = RealtimeClient("http://localhost:8080", "ak_test123")
        url = rt._ws_url()
        assert url.startswith("ws://localhost:8080/ws?token=")
        assert "ak_test123" in url

    def test_ws_url_https(self):
        rt = RealtimeClient("https://api.alancoin.network", "ak_prod")
        url = rt._ws_url()
        assert url.startswith("wss://api.alancoin.network/ws?token=")

    def test_ws_url_trailing_slash(self):
        rt = RealtimeClient("http://localhost:8080/", "ak_test")
        url = rt._ws_url()
        assert "//ws" not in url
        assert "/ws?" in url

    def test_stats_initial(self):
        rt = RealtimeClient("http://localhost:8080", "ak_test")
        s = rt.stats()
        assert s.events_received == 0
        assert s.reconnects == 0

    def test_receives_events(self):
        messages = [
            make_event_json("transaction", {"from": "0xA", "amount": "5.00"}),
            make_event_json("escrow_created", {"escrowId": "esc_1"}),
        ]
        mock_ws = MockWebSocket(messages)
        received = []

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_event=lambda e: received.append(e),
                )
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()

        run_async(_run())
        assert len(received) == 2
        assert received[0].type == "transaction"
        assert received[0].data["from"] == "0xA"
        assert received[1].type == "escrow_created"

    def test_sends_subscription_on_connect(self):
        mock_ws = MockWebSocket([])
        sub = RealtimeSubscription(
            all_events=False,
            event_types=[EventType.TRANSACTION],
        )

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    subscription=sub,
                )
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()

        run_async(_run())
        assert len(mock_ws.sent) >= 1
        sent_data = json.loads(mock_ws.sent[0])
        assert sent_data["allEvents"] is False
        assert sent_data["eventTypes"] == ["transaction"]

    def test_malformed_events_skipped(self):
        messages = [
            "not-json",
            make_event_json("transaction", {"valid": True}),
        ]
        mock_ws = MockWebSocket(messages)
        received = []

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_event=lambda e: received.append(e),
                )
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()

        run_async(_run())
        assert len(received) == 1
        assert received[0].type == "transaction"

    def test_stats_after_events(self):
        messages = [make_event_json("transaction") for _ in range(3)]
        mock_ws = MockWebSocket(messages)

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_event=lambda e: None,
                )
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()
            return rt.stats()

        s = run_async(_run())
        assert s.events_received == 3

    def test_close_is_idempotent(self):
        mock_ws = MockWebSocket([])

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient("http://localhost:8080", "ak_test")
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()
                await rt.close()  # should not raise

        run_async(_run())

    def test_context_manager(self):
        messages = [make_event_json("agent_joined")]
        mock_ws = MockWebSocket(messages)
        received = []

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                async with RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_event=lambda e: received.append(e),
                ) as rt:
                    await asyncio.sleep(0.05)

        run_async(_run())
        assert len(received) == 1

    def test_no_reconnect_when_error_handler_returns_false(self):
        mock_ws = MockWebSocket(raise_on_iter=ConnectionError("test"))
        connect_count = 0

        async def mock_connect(*args, **kwargs):
            nonlocal connect_count
            connect_count += 1
            return mock_ws

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(side_effect=mock_connect)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_error=lambda exc: False,
                    reconnect_backoff=0.01,
                )
                await rt.start()
                await asyncio.sleep(0.1)
                await rt.close()

        run_async(_run())
        assert connect_count == 1

    def test_subscribe_updates_live_connection(self):
        messages = [make_event_json("transaction")]
        mock_ws = MockWebSocket(messages)

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient("http://localhost:8080", "ak_test")
                await rt.start()
                await asyncio.sleep(0.02)

                new_sub = RealtimeSubscription(
                    all_events=False,
                    event_types=[EventType.ESCROW_CREATED],
                )
                await rt.subscribe(new_sub)
                await asyncio.sleep(0.02)
                await rt.close()

        run_async(_run())
        # Should have sent initial subscription + update
        assert len(mock_ws.sent) >= 2
        update = json.loads(mock_ws.sent[-1])
        assert update["eventTypes"] == ["escrow_created"]

    def test_on_event_exception_does_not_crash(self):
        messages = [
            make_event_json("transaction"),
            make_event_json("escrow_created"),
        ]
        mock_ws = MockWebSocket(messages)
        call_count = 0

        def bad_handler(e):
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                raise ValueError("handler error")

        async def _run():
            with patch("alancoin.realtime.websockets.connect", AsyncMock(return_value=mock_ws)):
                rt = RealtimeClient(
                    "http://localhost:8080", "ak_test",
                    on_event=bad_handler,
                )
                await rt.start()
                await asyncio.sleep(0.05)
                await rt.close()

        run_async(_run())
        assert call_count == 2


# ---------------------------------------------------------------------------
# Client integration tests
# ---------------------------------------------------------------------------


class TestClientIntegration:
    def test_realtime_returns_client(self):
        from alancoin.client import Alancoin

        client = Alancoin("http://localhost:8080", api_key="ak_test")
        rt = client.realtime(on_event=lambda e: None)
        assert isinstance(rt, RealtimeClient)

    def test_realtime_default_subscription(self):
        from alancoin.client import Alancoin

        client = Alancoin("http://localhost:8080", api_key="ak_test")
        rt = client.realtime()
        assert rt._subscription.all_events is True

    def test_realtime_custom_subscription(self):
        from alancoin.client import Alancoin

        client = Alancoin("http://localhost:8080", api_key="ak_test")
        sub = RealtimeSubscription(
            all_events=False,
            event_types=[EventType.TRANSACTION],
        )
        rt = client.realtime(subscription=sub)
        assert rt._subscription.all_events is False
        assert rt._subscription.event_types == ["transaction"]
