"""
Tests for critical production-readiness gaps in the Alancoin Python SDK.

Coverage targets:
  - Budget arithmetic (BudgetSession.pay / call_service)
  - BudgetSession._select_service strategies
  - BudgetSession._resolve_refs pipeline references
  - AlancoinError properties (funds_status, recovery, __str__)
  - NetworkError and PaymentRequiredError fields
  - _parse_decimal / _parse_duration_to_secs
  - GatewaySession.call with negative / zero amountPaid
  - GatewaySession._open when server returns no session ID
  - connect() / spend() network failure paths
  - Concurrent nonce monotonicity on SessionKeyManager
  - create_transaction_message / create_delegation_message format
  - ServiceResult dict interface
  - Budget dataclass defaults
  - _request() error-path coverage (500, 204, non-JSON body)
  - Idempotency key forwarding through gateway_proxy
"""

import threading
import time
from decimal import Decimal
from unittest.mock import MagicMock, patch, call

import pytest
import responses
from responses import matchers

from alancoin.admin import Alancoin
from alancoin import GatewaySession, Budget, AlancoinError, PolicyDeniedError
from alancoin.exceptions import (
    NetworkError,
    PaymentRequiredError,
    ValidationError,
    AgentNotFoundError,
)
from alancoin.session import (
    BudgetSession,
    ServiceResult,
    _parse_decimal,
)
from alancoin.client import _parse_duration_to_secs
from alancoin.session_keys import (
    SessionKeyManager,
    create_transaction_message,
    create_delegation_message,
)

BASE_URL = "http://localhost:8080"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def make_client(api_key="ak_test"):
    return Alancoin(base_url=BASE_URL, api_key=api_key)


def make_service_listing(price="0.50", reputation_score=0.5, agent_address="0xseller"):
    from alancoin.models import ServiceListing
    return ServiceListing(
        id="svc_1",
        type="translation",
        name="Translate",
        price=price,
        description="",
        endpoint="",
        active=True,
        agent_address=agent_address,
        agent_name="Bot",
        reputation_score=reputation_score,
    )


# ===========================================================================
# _parse_decimal
# ===========================================================================

class TestParseDecimal:
    """Unit-test the _parse_decimal helper used throughout BudgetSession."""

    def test_valid_integer_string(self):
        assert _parse_decimal("5", "amount") == Decimal("5")

    def test_valid_decimal_string(self):
        assert _parse_decimal("1.23456", "amount") == Decimal("1.23456")

    def test_zero(self):
        assert _parse_decimal("0", "amount") == Decimal("0")

    def test_negative_value_parses(self):
        # _parse_decimal does not validate sign — that is the caller's job
        assert _parse_decimal("-1.00", "amount") == Decimal("-1.00")

    def test_empty_string_raises(self):
        with pytest.raises(ValidationError, match="not a valid decimal"):
            _parse_decimal("", "amount")

    def test_non_numeric_raises(self):
        with pytest.raises(ValidationError, match="not a valid decimal"):
            _parse_decimal("abc", "price")

    def test_none_raises(self):
        with pytest.raises(ValidationError, match="not a valid decimal"):
            _parse_decimal(None, "amount")  # type: ignore[arg-type]

    def test_field_name_in_error_message(self):
        with pytest.raises(ValidationError, match="Invalid my_field"):
            _parse_decimal("bad", "my_field")


# ===========================================================================
# _parse_duration_to_secs
# ===========================================================================

class TestParseDurationToSecs:
    """Unit-test the _parse_duration_to_secs helper used in create_gateway_session."""

    def test_seconds(self):
        assert _parse_duration_to_secs("30s") == 30

    def test_minutes(self):
        assert _parse_duration_to_secs("10m") == 600

    def test_hours(self):
        assert _parse_duration_to_secs("2h") == 7200

    def test_days(self):
        assert _parse_duration_to_secs("7d") == 604800

    def test_case_insensitive_H(self):
        assert _parse_duration_to_secs("1H") == 3600

    def test_case_insensitive_D(self):
        assert _parse_duration_to_secs("1D") == 86400

    def test_pure_integer_string_returns_as_seconds(self):
        assert _parse_duration_to_secs("3600") == 3600

    def test_invalid_unit_returns_default_3600(self):
        assert _parse_duration_to_secs("5x") == 3600

    def test_whitespace_is_stripped(self):
        assert _parse_duration_to_secs("  1h  ") == 3600


# ===========================================================================
# AlancoinError properties and __str__
# ===========================================================================

class TestAlancoinError:
    """AlancoinError fields and convenience properties."""

    def test_str_with_code(self):
        err = AlancoinError("something broke", code="broken")
        assert str(err) == "[broken] something broke"

    def test_str_without_code(self):
        err = AlancoinError("plain message")
        assert str(err) == "plain message"

    def test_funds_status_from_details(self):
        err = AlancoinError("fail", details={"funds_status": "held_pending"})
        assert err.funds_status == "held_pending"

    def test_funds_status_defaults_empty_string(self):
        err = AlancoinError("fail")
        assert err.funds_status == ""

    def test_recovery_from_details(self):
        err = AlancoinError("fail", details={"recovery": "retry in 10s"})
        assert err.recovery == "retry in 10s"

    def test_recovery_defaults_empty_string(self):
        err = AlancoinError("fail")
        assert err.recovery == ""

    def test_details_none_becomes_empty_dict(self):
        err = AlancoinError("fail", details=None)
        assert err.details == {}

    def test_is_exception(self):
        err = AlancoinError("fail")
        assert isinstance(err, Exception)


class TestNetworkError:
    """NetworkError wraps the original exception."""

    def test_original_error_stored(self):
        original = ConnectionError("timeout")
        err = NetworkError("Request failed: timeout", original_error=original)
        assert err.original_error is original

    def test_status_code_is_none(self):
        err = NetworkError("fail")
        assert err.status_code is None

    def test_code_is_network_error(self):
        err = NetworkError("fail")
        assert err.code == "network_error"

    def test_is_alancoin_error(self):
        err = NetworkError("fail")
        assert isinstance(err, AlancoinError)


class TestPaymentRequiredError:
    """PaymentRequiredError carries payment details."""

    def test_fields(self):
        err = PaymentRequiredError(
            price="1.00",
            recipient="0xseller",
            currency="USDC",
            chain="base-sepolia",
            contract="0xcontract",
        )
        assert err.price == "1.00"
        assert err.recipient == "0xseller"
        assert err.currency == "USDC"
        assert err.chain == "base-sepolia"
        assert err.contract == "0xcontract"
        assert err.status_code == 402

    def test_default_currency_and_chain(self):
        err = PaymentRequiredError(price="2.00", recipient="0xrecip")
        assert err.currency == "USDC"
        assert err.chain == "base-sepolia"


# ===========================================================================
# ServiceResult dict interface
# ===========================================================================

class TestServiceResult:
    """ServiceResult must behave like a read-only dict for callers."""

    @pytest.fixture
    def result(self):
        return ServiceResult(
            data={"output": "hola", "status": "ok"},
            tx_hash="0xabc",
        )

    def test_getitem(self, result):
        assert result["output"] == "hola"

    def test_getitem_missing_raises_key_error(self, result):
        with pytest.raises(KeyError):
            _ = result["nonexistent"]

    def test_contains(self, result):
        assert "output" in result
        assert "missing" not in result

    def test_get_with_default(self, result):
        assert result.get("output") == "hola"
        assert result.get("missing", "default") == "default"

    def test_keys(self, result):
        assert set(result.keys()) == {"output", "status"}

    def test_values(self, result):
        assert set(result.values()) == {"hola", "ok"}

    def test_items(self, result):
        assert dict(result.items()) == {"output": "hola", "status": "ok"}

    def test_tx_hash_attribute(self, result):
        assert result.tx_hash == "0xabc"

    def test_repr_contains_service_name(self):
        svc = make_service_listing()
        r = ServiceResult(data={"output": "hi"}, service=svc)
        assert "Translate" in repr(r)

    def test_repr_without_service(self, result):
        # Should not raise
        repr(result)


# ===========================================================================
# Budget dataclass
# ===========================================================================

class TestBudget:
    """Budget defaults and field assignment."""

    def test_defaults(self):
        b = Budget()
        assert b.max_total == "10.00"
        assert b.max_per_tx == "1.00"
        assert b.max_per_day is None
        assert b.expires_in == "1h"
        assert b.allowed_services is None
        assert b.allowed_recipients is None

    def test_custom_values(self):
        b = Budget(
            max_total="50.00",
            max_per_tx="5.00",
            max_per_day="20.00",
            expires_in="24h",
            allowed_services=["translation"],
            allowed_recipients=["0xrecip"],
        )
        assert b.max_total == "50.00"
        assert b.max_per_tx == "5.00"
        assert b.max_per_day == "20.00"
        assert b.allowed_services == ["translation"]
        assert b.allowed_recipients == ["0xrecip"]


# ===========================================================================
# BudgetSession._select_service — all three strategies
# ===========================================================================

class TestSelectService:
    """BudgetSession._select_service picks the right listing per strategy."""

    def _make_session(self):
        client = MagicMock()
        budget = Budget(max_total="10.00", max_per_tx="5.00")
        s = BudgetSession(client, budget)
        return s

    def _listings(self):
        from alancoin.models import ServiceListing
        def make(price, score, aid):
            return ServiceListing(
                id=f"svc_{price}",
                type="translation",
                name=f"Bot_{price}",
                price=price,
                description="",
                endpoint="",
                active=True,
                agent_address=aid,
                agent_name=f"Agent_{price}",
                reputation_score=score,
            )
        return [
            make("1.00", 0.8, "0xhigh_rep_expensive"),
            make("0.25", 0.3, "0xcheap_low_rep"),
            make("0.50", 0.9, "0xbest_value"),
        ]

    def test_cheapest_strategy(self):
        s = self._make_session()
        listings = self._listings()
        chosen = s._select_service(listings, "cheapest")
        assert chosen.price == "0.25"

    def test_reputation_strategy(self):
        s = self._make_session()
        listings = self._listings()
        chosen = s._select_service(listings, "reputation")
        assert chosen.reputation_score == 0.9

    def test_best_value_strategy(self):
        s = self._make_session()
        listings = self._listings()
        # 0.9/0.50 = 1.8, 0.8/1.00 = 0.8, 0.3/0.25 = 1.2
        chosen = s._select_service(listings, "best_value")
        assert chosen.agent_address == "0xbest_value"

    def test_unknown_strategy_falls_back_to_cheapest(self):
        s = self._make_session()
        listings = self._listings()
        chosen = s._select_service(listings, "nonexistent_strategy")
        assert chosen.price == "0.25"

    def test_empty_listings_raises(self):
        s = self._make_session()
        with pytest.raises(AlancoinError, match="No services"):
            s._select_service([], "cheapest")

    def test_best_value_with_zero_price_uses_epsilon(self):
        """Zero price must not cause division by zero in best_value."""
        from alancoin.models import ServiceListing
        zero_price_svc = ServiceListing(
            id="svc_zero",
            type="t",
            name="zero",
            price="0",
            description="",
            endpoint="",
            active=True,
            agent_address="0x0",
            agent_name="A",
            reputation_score=1.0,
        )
        s = self._make_session()
        # Should not raise ZeroDivisionError
        chosen = s._select_service([zero_price_svc], "best_value")
        assert chosen.price == "0"

    def test_single_listing_all_strategies_return_it(self):
        s = self._make_session()
        single = [make_service_listing(price="1.00", reputation_score=0.5)]
        for strategy in ("cheapest", "reputation", "best_value"):
            chosen = s._select_service(single, strategy)
            assert chosen.price == "1.00"


# ===========================================================================
# BudgetSession._resolve_refs
# ===========================================================================

class TestResolveRefs:
    """Pipeline $prev reference resolution."""

    def test_prev_direct_reference(self):
        resolved = BudgetSession._resolve_refs(
            {"text": "$prev", "target": "es"},
            prev_output="summarized text",
        )
        assert resolved["text"] == "summarized text"
        assert resolved["target"] == "es"

    def test_prev_dot_key_reference(self):
        resolved = BudgetSession._resolve_refs(
            {"text": "$prev.output"},
            prev_output={"output": "hello", "lang": "en"},
        )
        assert resolved["text"] == "hello"

    def test_prev_dot_key_missing_raises(self):
        with pytest.raises(AlancoinError, match="pipeline_ref_error"):
            BudgetSession._resolve_refs(
                {"text": "$prev.output"},
                prev_output={"no_output": "x"},
            )

    def test_prev_dot_key_on_non_dict_raises(self):
        with pytest.raises(AlancoinError, match="pipeline_ref_error"):
            BudgetSession._resolve_refs(
                {"text": "$prev.output"},
                prev_output="plain string",
            )

    def test_non_ref_values_pass_through(self):
        resolved = BudgetSession._resolve_refs(
            {"a": "literal", "b": 42, "c": True},
            prev_output="ignored",
        )
        assert resolved == {"a": "literal", "b": 42, "c": True}

    def test_empty_params(self):
        resolved = BudgetSession._resolve_refs({}, prev_output="anything")
        assert resolved == {}

    def test_multiple_refs_in_same_params(self):
        resolved = BudgetSession._resolve_refs(
            {"x": "$prev.foo", "y": "$prev.bar"},
            prev_output={"foo": 1, "bar": 2},
        )
        assert resolved == {"x": 1, "y": 2}

    def test_prev_is_whole_dict_when_used_directly(self):
        d = {"output": "val"}
        resolved = BudgetSession._resolve_refs({"data": "$prev"}, prev_output=d)
        assert resolved["data"] is d


# ===========================================================================
# BudgetSession.pay — budget enforcement
# ===========================================================================

class TestBudgetSessionPay:
    """BudgetSession.pay enforces per-tx and total budget limits."""

    def _make_active_session(self, max_total="10.00", max_per_tx="2.00"):
        client = MagicMock()
        client.address = "0xbuyer"
        budget = Budget(max_total=max_total, max_per_tx=max_per_tx)
        s = BudgetSession(client, budget)
        s._active = True
        # Give it a real (mocked) SKM
        s._skm = MagicMock()
        s._skm.transact.return_value = {"txHash": "0xdeadbeef", "status": "ok"}
        s._key_id = "sk_123"
        return s

    def test_pay_when_inactive_raises(self):
        client = MagicMock()
        budget = Budget()
        s = BudgetSession(client, budget)
        s._active = False
        with pytest.raises(AlancoinError, match="not active"):
            s.pay("0xrecip", "1.00")

    def test_pay_exceeds_per_tx_limit_raises(self):
        s = self._make_active_session(max_per_tx="1.00")
        with pytest.raises(AlancoinError) as exc:
            s.pay("0xrecip", "1.50")
        assert exc.value.code == "per_tx_limit_exceeded"

    def test_pay_at_exact_per_tx_limit_succeeds(self):
        s = self._make_active_session(max_per_tx="1.00")
        result = s.pay("0xrecip", "1.00")
        assert result["txHash"] == "0xdeadbeef"

    def test_pay_exceeds_total_budget_raises(self):
        s = self._make_active_session(max_total="1.00", max_per_tx="2.00")
        # First payment brings us to 0.80
        s._total_spent = Decimal("0.80")
        with pytest.raises(AlancoinError) as exc:
            s.pay("0xrecip", "0.25")  # 0.80 + 0.25 = 1.05 > 1.00
        assert exc.value.code == "budget_exceeded"

    def test_pay_accumulates_total_spent(self):
        s = self._make_active_session(max_total="10.00", max_per_tx="5.00")
        s.pay("0xrecip", "1.50")
        s.pay("0xrecip", "2.50")
        assert s.total_spent == "4.00"
        assert s.tx_count == 2

    def test_pay_invalid_amount_raises_validation_error(self):
        s = self._make_active_session()
        with pytest.raises(ValidationError, match="not a valid decimal"):
            s.pay("0xrecip", "not-a-number")

    def test_pay_re_raises_alancoin_error_from_skm(self):
        s = self._make_active_session()
        s._skm.transact.side_effect = AlancoinError("insufficient funds", code="balance_low")
        with pytest.raises(AlancoinError, match="insufficient funds"):
            s.pay("0xrecip", "1.00")
        # total_spent must NOT have increased (compare numerically — Decimal string repr may vary)
        assert Decimal(s.total_spent) == Decimal("0")

    def test_pay_wraps_generic_exception_as_alancoin_error(self):
        s = self._make_active_session()
        s._skm.transact.side_effect = RuntimeError("unexpected crash")
        with pytest.raises(AlancoinError) as exc:
            s.pay("0xrecip", "1.00")
        assert exc.value.code == "transact_failed"
        # funds_status should indicate ambiguity
        assert exc.value.funds_status == "unknown"

    def test_remaining_decreases_after_payments(self):
        s = self._make_active_session(max_total="5.00", max_per_tx="5.00")
        s.pay("0xrecip", "1.00")
        assert Decimal(s.remaining) == Decimal("4.00")

    def test_zero_amount_payment_succeeds(self):
        """Zero-amount payments are not blocked by budget math."""
        s = self._make_active_session()
        result = s.pay("0xrecip", "0.00")
        assert result is not None


# ===========================================================================
# BudgetSession.call_service — budget checks with mocked client
# ===========================================================================

class TestBudgetSessionCallService:
    """call_service enforces budget before and after discovery."""

    def _make_session(self, max_total="10.00", max_per_tx="2.00"):
        client = MagicMock()
        client.address = "0xbuyer"
        budget = Budget(max_total=max_total, max_per_tx=max_per_tx)
        s = BudgetSession(client, budget)
        s._active = True
        s._skm = MagicMock()
        s._skm.transact.return_value = {"txHash": "0xhash"}
        s._key_id = "sk_abc"
        return s

    def test_call_service_inactive_raises(self):
        s = self._make_session()
        s._active = False
        with pytest.raises(AlancoinError, match="not active"):
            s.call_service("translation", text="hi")

    def test_call_service_no_services_found_raises(self):
        s = self._make_session()
        s._client.discover.return_value = []
        with pytest.raises(AlancoinError) as exc:
            s.call_service("translation", text="hi")
        assert exc.value.code == "no_services"

    def test_call_service_price_exceeds_per_tx_raises(self):
        s = self._make_session(max_per_tx="1.00")
        expensive = make_service_listing(price="1.50")
        s._client.discover.return_value = [expensive]
        with pytest.raises(AlancoinError) as exc:
            s.call_service("translation", text="hi")
        assert exc.value.code == "per_tx_limit_exceeded"

    def test_call_service_price_exceeds_remaining_budget_raises(self):
        s = self._make_session(max_total="1.00", max_per_tx="2.00")
        s._total_spent = Decimal("0.80")
        svc = make_service_listing(price="0.30")
        s._client.discover.return_value = [svc]
        with pytest.raises(AlancoinError) as exc:
            s.call_service("translation", text="hi")
        assert exc.value.code == "budget_exceeded"

    def test_call_service_escrow_false_uses_direct_payment(self):
        s = self._make_session()
        svc = make_service_listing(price="0.50", agent_address="0xseller")
        svc.endpoint = ""  # No endpoint — just payment
        s._client.discover.return_value = [svc]

        result = s.call_service("translation", text="hi", escrow=False)

        # Should have called pay (which calls skm.transact)
        s._skm.transact.assert_called_once()
        assert result["paid"] is True

    def test_call_service_escrow_true_calls_create_escrow(self):
        s = self._make_session()
        svc = make_service_listing(price="0.50")
        svc.endpoint = ""  # No endpoint
        s._client.discover.return_value = [svc]
        s._client.create_escrow.return_value = {"escrow": {"id": "esc_001"}}
        s._client.confirm_escrow.return_value = {}

        result = s.call_service("translation", text="hi", escrow=True)

        s._client.create_escrow.assert_called_once()
        s._client.confirm_escrow.assert_called_once_with("esc_001")
        assert result.escrow_id == "esc_001"

    def test_call_service_with_endpoint_calls_confirm_on_success(self):
        s = self._make_session()
        svc = make_service_listing(price="0.50")
        svc.endpoint = "http://service.example/translate"
        s._client.discover.return_value = [svc]
        s._client.create_escrow.return_value = {"escrow": {"id": "esc_002"}}
        s._client.confirm_escrow.return_value = {}

        with patch("requests.post") as mock_post:
            mock_resp = MagicMock()
            mock_resp.json.return_value = {"output": "hola"}
            mock_resp.raise_for_status = MagicMock()
            mock_post.return_value = mock_resp

            result = s.call_service("translation", text="hi")

        assert result["output"] == "hola"
        s._client.confirm_escrow.assert_called_once_with("esc_002")

    def test_call_service_endpoint_no_output_does_not_confirm_escrow(self):
        """Escrow must NOT be confirmed when service returns no 'output'."""
        s = self._make_session()
        svc = make_service_listing(price="0.50")
        svc.endpoint = "http://service.example/translate"
        s._client.discover.return_value = [svc]
        s._client.create_escrow.return_value = {"escrow": {"id": "esc_003"}}

        with patch("requests.post") as mock_post:
            mock_resp = MagicMock()
            mock_resp.json.return_value = {"status": "done"}  # no "output" key
            mock_resp.raise_for_status = MagicMock()
            mock_post.return_value = mock_resp

            result = s.call_service("translation", text="hi")

        # Escrow must not have been confirmed
        s._client.confirm_escrow.assert_not_called()
        assert "_escrow_warning" in result

    def test_call_service_budget_spent_updates_correctly_with_escrow(self):
        s = self._make_session(max_total="5.00")
        svc = make_service_listing(price="1.00")
        svc.endpoint = ""
        s._client.discover.return_value = [svc]
        s._client.create_escrow.return_value = {"escrow": {"id": "esc_004"}}
        s._client.confirm_escrow.return_value = {}

        s.call_service("translation")
        assert Decimal(s.total_spent) == Decimal("1.00")
        assert s.tx_count == 1


# ===========================================================================
# GatewaySession.call — edge cases
# ===========================================================================

class TestGatewaySessionCallEdgeCases:
    """GatewaySession.call handles server response variations."""

    @pytest.fixture
    def client(self):
        return make_client()

    @responses.activate
    def test_call_with_zero_amount_paid_does_not_increase_spent(self, client):
        """amountPaid=0 leaves total_spent at zero (cost >= 0 adds 0, net effect is zero)."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_zero", "maxTotal": "5.00", "status": "active"}},
            status=201,
        )
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/proxy",
            json={"result": {"response": {"output": "ok"}, "amountPaid": "0.000000"}},
            status=200,
        )
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_zero",
            json={"session": {"id": "gw_zero", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            gw.call("translation")
            # Decimal("0.000000") == Decimal("0") — budget is still intact
            assert Decimal(gw.total_spent) == Decimal("0")

    @responses.activate
    def test_call_with_missing_amount_paid_does_not_crash(self, client):
        """Missing amountPaid must be handled gracefully."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_no_amt", "maxTotal": "5.00", "status": "active"}},
            status=201,
        )
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/proxy",
            json={"result": {"response": {"output": "ok"}}},  # No amountPaid
            status=200,
        )
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_no_amt",
            json={"session": {"id": "gw_no_amt", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            result = gw.call("translation")
            assert result is not None  # Should not crash
            assert gw.total_spent == "0"

    @responses.activate
    def test_call_attaches_gateway_metadata(self, client):
        """Response dict must carry _gateway metadata."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_meta", "maxTotal": "5.00", "status": "active"}},
            status=201,
        )
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "result"},
                    "amountPaid": "0.10",
                    "serviceUsed": "0xsvc",
                    "serviceName": "translator",
                    "latencyMs": 55,
                }
            },
            status=200,
        )
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_meta",
            json={"session": {"id": "gw_meta", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            result = gw.call("translation")
        assert result["_gateway"]["amountPaid"] == "0.10"
        assert result["_gateway"]["serviceName"] == "translator"
        assert result["_gateway"]["latencyMs"] == 55

    @responses.activate
    def test_open_raises_when_server_returns_no_session_id(self, client):
        """_open must raise AlancoinError if session.id is absent."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {}},  # Empty session — no id
            status=201,
        )

        with pytest.raises(AlancoinError, match="no gateway session ID"):
            with client.gateway(max_total="5.00") as gw:
                pass

    @responses.activate
    def test_idempotency_key_forwarded_to_proxy(self, client):
        """idempotency_key must be included in the proxy request body."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_idem", "maxTotal": "5.00", "status": "active"}},
            status=201,
        )
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/proxy",
            match=[matchers.json_params_matcher({
                "serviceType": "translation",
                "params": {"text": "hello"},
                "idempotencyKey": "idem_key_123",
            })],
            json={"result": {"response": {"output": "hola"}, "amountPaid": "0.50"}},
            status=200,
        )
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_idem",
            json={"session": {"id": "gw_idem", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            result = gw.call("translation", idempotency_key="idem_key_123", text="hello")
        assert result["output"] == "hola"


# ===========================================================================
# _request() HTTP error paths
# ===========================================================================

class TestRequestErrorPaths:
    """Alancoin._request() handles diverse HTTP error scenarios."""

    @pytest.fixture
    def client(self):
        return make_client()

    @responses.activate
    def test_500_raises_alancoin_error_with_status_code(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/health",
            json={"error": "internal_error", "message": "DB connection failed"},
            status=500,
        )
        with pytest.raises(AlancoinError) as exc:
            client.health()
        assert exc.value.status_code == 500
        assert exc.value.code == "internal_error"

    @responses.activate
    def test_500_with_non_json_body_raises(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/health",
            body="Internal Server Error",
            status=500,
            content_type="text/plain",
        )
        with pytest.raises(AlancoinError) as exc:
            client.health()
        assert exc.value.status_code == 500

    @responses.activate
    def test_204_returns_empty_dict_from_internal_request(self, client):
        """_request() must return {} for 204 No Content responses."""
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/agents/0xaddr",
            status=204,
        )
        # delete_agent() itself returns None (documented), but internally
        # _request() must return {} and not raise for 204.
        result = client._request("DELETE", "/v1/agents/0xaddr")
        assert result == {}

    @responses.activate
    def test_network_exception_raises_network_error(self, client):
        import requests as _requests
        responses.add(
            responses.GET,
            f"{BASE_URL}/health",
            body=_requests.exceptions.ConnectionError("connection refused"),
        )
        with pytest.raises(NetworkError) as exc:
            client.health()
        assert isinstance(exc.value.original_error, _requests.exceptions.ConnectionError)

    @responses.activate
    def test_402_raises_payment_required_error(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/services",
            json={
                "price": "0.01",
                "recipient": "0xplatform",
                "currency": "USDC",
                "chain": "base-sepolia",
            },
            status=402,
        )
        with pytest.raises(PaymentRequiredError) as exc:
            client.discover(service_type="translation")
        assert exc.value.price == "0.01"
        assert exc.value.recipient == "0xplatform"

    @responses.activate
    def test_402_malformed_json_raises_payment_required_error(self, client):
        """Malformed 402 body must still produce PaymentRequiredError."""
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/services",
            body="Payment Required",
            status=402,
            content_type="text/plain",
        )
        with pytest.raises(PaymentRequiredError):
            client.discover(service_type="translation")

    @responses.activate
    def test_server_returns_extra_details_preserved(self, client):
        """Extra fields beyond error/message must appear in AlancoinError.details."""
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={
                "error": "rate_limited",
                "message": "Too many requests",
                "funds_status": "no_change",
                "recovery": "Retry after 60s",
            },
            status=429,
        )
        with pytest.raises(AlancoinError) as exc:
            client.create_gateway_session(max_total="5.00")
        assert exc.value.funds_status == "no_change"
        assert exc.value.recovery == "Retry after 60s"

    @responses.activate
    def test_success_with_non_json_body_returns_empty_dict(self, client):
        """200 response with non-JSON body returns {} without raising."""
        responses.add(
            responses.GET,
            f"{BASE_URL}/health",
            body="OK",
            status=200,
            content_type="text/plain",
        )
        result = client.health()
        assert result == {}

    @responses.activate
    def test_url_construction_strips_trailing_slash(self, client):
        """Trailing slash on base_url must not produce double-slash URLs."""
        client2 = Alancoin(base_url="http://localhost:8080/", api_key="ak_test")
        responses.add(
            responses.GET,
            "http://localhost:8080/health",
            json={"status": "ok"},
            status=200,
        )
        result = client2.health()
        assert result["status"] == "ok"
        # Verify the actual request URL has no double slash
        assert "//" not in responses.calls[0].request.url.replace("http://", "")


# ===========================================================================
# connect() / spend() — network failure paths
# ===========================================================================

class TestConnectNetworkFailure:
    """connect() and spend() propagate errors correctly."""

    @responses.activate
    def test_connect_raises_when_create_session_fails(self):
        from alancoin import connect

        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"error": "server_error", "message": "DB down"},
            status=503,
        )

        with pytest.raises(AlancoinError):
            with connect(BASE_URL, api_key="ak_test", budget="5.00") as gw:
                pass  # Should not reach here

    @responses.activate
    def test_connect_propagates_policy_denied_error(self):
        from alancoin import connect

        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"error": "policy_denied", "message": "Blocked by admin", "contact": "ops@x.io"},
            status=403,
        )

        with pytest.raises(PolicyDeniedError) as exc:
            with connect(BASE_URL, api_key="ak_test", budget="5.00") as gw:
                pass
        assert exc.value.contact == "ops@x.io"

    @responses.activate
    def test_spend_propagates_proxy_error(self):
        from alancoin import spend

        # Session creates fine
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_spend_err", "maxTotal": "5.00", "status": "active"}},
            status=201,
        )
        # Proxy call fails
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/proxy",
            json={"error": "no_services", "message": "No translation services"},
            status=503,
        )
        # Session closes after error
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_spend_err",
            json={"session": {"id": "gw_spend_err", "status": "closed"}},
            status=200,
        )

        with pytest.raises(AlancoinError):
            spend(BASE_URL, api_key="ak_test", service_type="translation", budget="5.00")

        # Verify session was still closed
        assert any(c.request.method == "DELETE" for c in responses.calls)


# ===========================================================================
# SessionKeyManager — nonce monotonicity and concurrency
# ===========================================================================

class TestSessionKeyManagerNonce:
    """SessionKeyManager.next_nonce is monotonic and thread-safe."""

    @pytest.fixture
    def skm(self):
        # Bypass crypto import requirement by mocking the module-level flag
        with patch("alancoin.session_keys.HAS_ETH_ACCOUNT", True):
            with patch("alancoin.session_keys.generate_session_keypair",
                       return_value=("0xprivkey", "0xpubkey")):
                return SessionKeyManager()

    def test_initial_nonce_starts_at_zero(self, skm):
        assert skm._nonce == 0

    def test_next_nonce_increments(self, skm):
        n1 = skm.next_nonce
        n2 = skm.next_nonce
        n3 = skm.next_nonce
        assert n1 == 1
        assert n2 == 2
        assert n3 == 3

    def test_next_nonce_is_monotonic_under_concurrency(self, skm):
        """No two goroutines should get the same nonce."""
        nonces = []
        lock = threading.Lock()

        def grab_nonce():
            n = skm.next_nonce
            with lock:
                nonces.append(n)

        threads = [threading.Thread(target=grab_nonce) for _ in range(50)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert len(nonces) == 50
        # All nonces must be unique
        assert len(set(nonces)) == 50
        # All nonces must be positive
        assert all(n > 0 for n in nonces)

    def test_set_key_id(self, skm):
        skm.set_key_id("sk_abc123")
        assert skm.key_id == "sk_abc123"

    def test_transact_raises_without_key_id(self, skm):
        """transact() must raise if key_id has not been set."""
        with pytest.raises(ValueError, match="key_id not set"):
            skm.transact(MagicMock(), "0xagent", "0xrecip", "1.00")


# ===========================================================================
# create_transaction_message / create_delegation_message — format contract
# ===========================================================================

class TestMessageFormats:
    """Server protocol message formats must never change silently."""

    def test_transaction_message_format(self):
        msg = create_transaction_message(
            to="0xRecipient",
            amount="1.50",
            nonce=42,
            timestamp=1700000000,
        )
        # Format: "Alancoin|{to.lower()}|{amount}|{nonce}|{timestamp}"
        assert msg == "Alancoin|0xrecipient|1.50|42|1700000000"

    def test_transaction_message_lowercases_address(self):
        msg = create_transaction_message("0xABCDEF", "0.01", 1, 0)
        assert "0xabcdef" in msg

    def test_delegation_message_format(self):
        msg = create_delegation_message(
            child_public_key="0xChildKey",
            max_total="5.00",
            nonce=7,
            timestamp=1700000001,
        )
        # Format: "AlancoinDelegate|{child.lower()}|{maxTotal}|{nonce}|{timestamp}"
        assert msg == "AlancoinDelegate|0xchildkey|5.00|7|1700000001"

    def test_delegation_message_lowercases_child_key(self):
        msg = create_delegation_message("0xUPPER", "1.00", 1, 0)
        assert "0xupper" in msg

    def test_transaction_message_parts_count(self):
        msg = create_transaction_message("0xaddr", "1.00", 1, 1234567890)
        parts = msg.split("|")
        assert len(parts) == 5
        assert parts[0] == "Alancoin"

    def test_delegation_message_parts_count(self):
        msg = create_delegation_message("0xchild", "2.00", 3, 9876543210)
        parts = msg.split("|")
        assert len(parts) == 5
        assert parts[0] == "AlancoinDelegate"


# ===========================================================================
# GatewaySession — concurrent call() thread safety
# ===========================================================================

class TestGatewaySessionThreadSafety:
    """GatewaySession._lock must prevent races on _total_spent / _request_count."""

    @responses.activate
    def test_concurrent_calls_accumulate_spend_correctly(self):
        client = make_client()

        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/gateway/sessions",
            json={"session": {"id": "gw_thread", "maxTotal": "100.00", "status": "active"}},
            status=201,
        )
        # Register 10 proxy responses, each charging 1.00
        for _ in range(10):
            responses.add(
                responses.POST,
                f"{BASE_URL}/v1/gateway/proxy",
                json={"result": {"response": {"output": "ok"}, "amountPaid": "1.000000"}},
                status=200,
            )
        responses.add(
            responses.DELETE,
            f"{BASE_URL}/v1/gateway/sessions/gw_thread",
            json={"session": {"id": "gw_thread", "status": "closed"}},
            status=200,
        )

        errors = []

        with client.gateway(max_total="100.00") as gw:
            def do_call():
                try:
                    gw.call("translation")
                except Exception as e:
                    errors.append(e)

            threads = [threading.Thread(target=do_call) for _ in range(10)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()

        assert errors == [], f"Unexpected errors: {errors}"
        assert Decimal(gw.total_spent) == Decimal("10.000000")
        assert gw.request_count == 10


# ===========================================================================
# Alancoin.register — auto-configures api_key from response
# ===========================================================================

class TestRegisterAutoConfiguresApiKey:
    """register() must auto-configure api_key on the client when not already set."""

    @responses.activate
    def test_register_sets_api_key_on_client(self):
        client = Alancoin(base_url=BASE_URL)  # No api_key
        assert client.api_key is None

        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/agents",
            json={
                "agent": {
                    "address": "0xagent",
                    "name": "NewAgent",
                    "services": [],
                    "stats": {},
                },
                "apiKey": "ak_newly_generated",
                "keyId": "key_123",
                "usage": "Use Bearer token",
            },
            status=201,
        )

        result = client.register(address="0xagent", name="NewAgent")

        assert result["apiKey"] == "ak_newly_generated"
        # Client should now use the key
        assert client.api_key == "ak_newly_generated"
        assert client._session.headers["Authorization"] == "Bearer ak_newly_generated"

    @responses.activate
    def test_register_does_not_overwrite_existing_api_key(self):
        client = Alancoin(base_url=BASE_URL, api_key="ak_existing")

        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/agents",
            json={
                "agent": {"address": "0xagent", "name": "A", "services": [], "stats": {}},
                "apiKey": "ak_new_from_server",
            },
            status=201,
        )

        client.register(address="0xagent", name="A")
        # Must NOT overwrite
        assert client.api_key == "ak_existing"
