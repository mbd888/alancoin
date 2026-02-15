"""Tests for GatewayMarket failure modes: 5xx propagation, malformed responses,
missing sessions, context manager cleanup, and teardown failures."""

import pytest
from unittest.mock import patch, MagicMock

from harness.clients.gateway_market import GatewayMarket, GatewayError, GatewaySession
from harness.clients.mock_market import ServiceType


def _make_market(**kwargs):
    """Create a GatewayMarket with agents and a service for testing."""
    m = GatewayMarket(api_url="http://fake:8080", api_key="sk_test", **kwargs)
    buyer = m.create_agent("Buyer", "buyer", balance=10.0, max_per_tx=1.0, max_per_day=5.0)
    seller = m.create_agent("Seller", "seller", balance=0.0)
    svc = m.add_service(seller.id, ServiceType.INFERENCE, "Test", "desc", 0.50)
    return m, buyer, seller, svc


class TestGateway5xxPropagation:
    """5xx errors should propagate, not be swallowed as rejections."""

    def test_500_propagates(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        with patch.object(m, "_post", side_effect=GatewayError(500, {"error": "internal"})):
            with pytest.raises(GatewayError) as exc_info:
                m.transact(buyer.id, seller.id, 0.50, svc.id)
            assert exc_info.value.status_code == 500

    def test_503_propagates(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        with patch.object(m, "_post", side_effect=GatewayError(503, {"error": "unavailable"})):
            with pytest.raises(GatewayError) as exc_info:
                m.transact(buyer.id, seller.id, 0.50, svc.id)
            assert exc_info.value.status_code == 503

    def test_403_becomes_rejection(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        with patch.object(m, "_post", side_effect=GatewayError(403, {"error": "policy_denied", "message": "rate limit"})):
            tx = m.transact(buyer.id, seller.id, 0.50, svc.id)
            assert tx.status == "rejected"
            assert "policy_denied" in tx.rejection_reason

    def test_422_becomes_rejection(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        with patch.object(m, "_post", side_effect=GatewayError(422, {"error": "budget_exceeded", "message": "over limit"})):
            tx = m.transact(buyer.id, seller.id, 0.50, svc.id)
            assert tx.status == "rejected"
            assert "budget_exceeded" in tx.rejection_reason


class TestMalformedGatewayResponse:
    """Missing required fields in gateway response should raise, not silently fallback."""

    def test_missing_total_spent_raises(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        # Response missing totalSpent
        with patch.object(m, "_post", return_value={"result": {"amountPaid": "0.50"}}):
            with pytest.raises(GatewayError, match="totalSpent"):
                m.transact(buyer.id, seller.id, 0.50, svc.id)

    def test_missing_amount_paid_raises(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        # Response missing result.amountPaid
        with patch.object(m, "_post", return_value={"totalSpent": "0.50", "result": {}}):
            with pytest.raises(GatewayError, match="amountPaid"):
                m.transact(buyer.id, seller.id, 0.50, svc.id)

    def test_missing_result_object_raises(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        # Response has totalSpent but no result key
        with patch.object(m, "_post", return_value={"totalSpent": "0.50"}):
            with pytest.raises(GatewayError, match="amountPaid"):
                m.transact(buyer.id, seller.id, 0.50, svc.id)

    def test_result_is_string_raises(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        # result is a string, not a dict
        with patch.object(m, "_post", return_value={"totalSpent": "0.50", "result": "ok"}):
            with pytest.raises(GatewayError, match="amountPaid"):
                m.transact(buyer.id, seller.id, 0.50, svc.id)

    def test_valid_response_updates_balances(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )
        with patch.object(m, "_post", return_value={
            "totalSpent": "0.50",
            "result": {"amountPaid": "0.50"},
        }):
            tx = m.transact(buyer.id, seller.id, 0.50, svc.id)
            assert tx.status == "accepted"
            assert buyer.balance == pytest.approx(9.50)
            assert seller.balance == pytest.approx(0.50)


class TestSessionRequired:
    """transact() should fail loudly if buyer has no session but others do."""

    def test_no_session_but_others_exist_raises(self):
        m = GatewayMarket(api_url="http://fake:8080", api_key="sk_test")
        b1 = m.create_agent("Buyer1", "buyer", balance=10.0)
        b2 = m.create_agent("Buyer2", "buyer", balance=10.0)
        seller = m.create_agent("Seller", "seller", balance=0.0)

        # Only b1 has a session
        m._sessions[b1.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=b1.id,
            max_total=10.0, max_per_request=1.0,
        )

        with pytest.raises(RuntimeError, match="setup_session"):
            m.transact(b2.id, seller.id, 0.50)

    def test_no_sessions_at_all_uses_local(self):
        m, buyer, seller, svc = _make_market()
        # No sessions — pure local mode
        tx = m.transact(buyer.id, seller.id, 0.50, svc.id)
        assert tx.status == "accepted"


class TestContextManager:
    """Context manager should auto-close all sessions on exit."""

    def test_exit_tears_down_sessions(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )

        with patch.object(m, "_delete", return_value={"totalSpent": "0"}) as mock_delete:
            with m:
                assert len(m._sessions) == 1
            # After exit, sessions are cleaned up
            assert len(m._sessions) == 0
            mock_delete.assert_called_once()

    def test_exit_on_exception_still_cleans_up(self):
        m, buyer, seller, svc = _make_market()
        m._sessions[buyer.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=buyer.id,
            max_total=10.0, max_per_request=1.0,
        )

        with patch.object(m, "_delete", return_value={"totalSpent": "0"}) as mock_delete:
            with pytest.raises(ValueError):
                with m:
                    raise ValueError("boom")
            # Sessions still cleaned up despite exception
            assert len(m._sessions) == 0
            mock_delete.assert_called_once()

    def test_teardown_failure_doesnt_prevent_other_teardowns(self):
        m = GatewayMarket(api_url="http://fake:8080", api_key="sk_test")
        b1 = m.create_agent("Buyer1", "buyer", balance=10.0)
        b2 = m.create_agent("Buyer2", "buyer", balance=10.0)
        m.create_agent("Seller", "seller", balance=0.0)

        m._sessions[b1.id] = GatewaySession(
            session_id="s1", token="t1", agent_id=b1.id,
            max_total=10.0, max_per_request=1.0,
        )
        m._sessions[b2.id] = GatewaySession(
            session_id="s2", token="t2", agent_id=b2.id,
            max_total=10.0, max_per_request=1.0,
        )

        call_count = 0

        def _delete_side_effect(path):
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                raise GatewayError(500, {"error": "cleanup failed"})
            return {"totalSpent": "0"}

        with patch.object(m, "_delete", side_effect=_delete_side_effect):
            with m:
                assert len(m._sessions) == 2
            # First teardown failed, second succeeded — one session removed
            # The failed one stays because _delete raised before del _sessions[id]
            # But the context manager catches the exception and continues
            assert call_count == 2  # Both attempted

    def test_no_sessions_noop(self):
        m, buyer, seller, svc = _make_market()
        with patch.object(m, "_delete") as mock_delete:
            with m:
                pass
            mock_delete.assert_not_called()
