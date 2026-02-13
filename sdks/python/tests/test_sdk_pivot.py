"""Tests for SDK pivot: PolicyDeniedError, connect()/spend(), and deprecation warnings."""

import warnings

import pytest
import responses
from responses import matchers

from alancoin.admin import Alancoin
from alancoin.exceptions import AlancoinError, PolicyDeniedError


# -----------------------------------------------------------------------------
# PolicyDeniedError + 403 handler
# -----------------------------------------------------------------------------

class TestPolicyDeniedError:
    """Test PolicyDeniedError exception and the 403 handler in _request()."""

    BASE_URL = "http://localhost:8080"

    @pytest.fixture
    def client(self):
        return Alancoin(base_url=self.BASE_URL, api_key="test_key")

    def test_policy_denied_error_fields(self):
        """Test PolicyDeniedError carries correct fields."""
        err = PolicyDeniedError(message="velocity limit exceeded", contact="ops@example.com")
        assert err.message == "velocity limit exceeded"
        assert err.contact == "ops@example.com"
        assert err.status_code == 403
        assert err.code == "policy_denied"
        assert isinstance(err, AlancoinError)

    def test_policy_denied_error_default_contact(self):
        """Test PolicyDeniedError defaults contact to empty string."""
        err = PolicyDeniedError(message="denied")
        assert err.contact == ""

    @responses.activate
    def test_403_policy_denied_raises(self, client):
        """Test that 403 with error=policy_denied raises PolicyDeniedError."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "error": "policy_denied",
                "message": "Velocity limit: 5 txns/min exceeded",
                "contact": "admin@platform.io",
            },
            status=403,
        )

        with pytest.raises(PolicyDeniedError) as exc_info:
            client.create_gateway_session(max_total="5.00")

        assert "Velocity limit" in exc_info.value.message
        assert exc_info.value.contact == "admin@platform.io"

    @responses.activate
    def test_403_non_policy_falls_through(self, client):
        """Test that 403 without error=policy_denied falls to generic handler."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/agents/0x123/balance",
            json={
                "error": "forbidden",
                "message": "Not authorized",
            },
            status=403,
        )

        with pytest.raises(AlancoinError) as exc_info:
            client.get_platform_balance("0x123")

        assert not isinstance(exc_info.value, PolicyDeniedError)
        assert exc_info.value.status_code == 403
        assert exc_info.value.code == "forbidden"

    @responses.activate
    def test_403_malformed_json_falls_through(self, client):
        """Test that 403 with unparseable body falls to generic handler."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            body="Access Denied",
            status=403,
            content_type="text/plain",
        )

        with pytest.raises(AlancoinError) as exc_info:
            client.create_gateway_session(max_total="5.00")

        assert not isinstance(exc_info.value, PolicyDeniedError)
        assert exc_info.value.status_code == 403


# -----------------------------------------------------------------------------
# connect() and spend()
# -----------------------------------------------------------------------------

class TestConnect:
    """Test the connect() context manager and spend() one-shot."""

    BASE_URL = "http://localhost:8080"

    def _mock_gateway_lifecycle(self, session_id="gw_connect"):
        """Register mock responses for gateway create + close."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": session_id,
                    "token": session_id,
                    "maxTotal": "5.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/{session_id}",
            json={"session": {"id": session_id, "status": "closed"}},
            status=200,
        )

    def _mock_proxy_call(self, output="hola", amount="0.500000"):
        """Register a mock proxy response."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": output},
                    "amountPaid": amount,
                    "serviceUsed": "0xseller",
                },
                "totalSpent": amount,
                "remaining": "4.500000",
                "requestCount": 1,
            },
            status=200,
        )

    @responses.activate
    def test_connect_opens_and_closes_session(self):
        """Test that connect() creates a gateway session and closes it on exit."""
        from alancoin import connect

        self._mock_gateway_lifecycle()

        with connect(self.BASE_URL, api_key="ak_test", budget="5.00") as gw:
            assert gw.is_active is True

        # POST create + DELETE close
        assert len(responses.calls) == 2
        assert responses.calls[0].request.method == "POST"
        assert responses.calls[1].request.method == "DELETE"

    @responses.activate
    def test_connect_yields_working_gateway(self):
        """Test that the yielded gateway can make proxy calls."""
        from alancoin import connect

        self._mock_gateway_lifecycle()
        self._mock_proxy_call()

        with connect(self.BASE_URL, api_key="ak_test", budget="5.00") as gw:
            result = gw.call("translation", text="hello", target="es")
            assert result["output"] == "hola"
            assert gw.total_spent == "0.500000"

    @responses.activate
    def test_connect_closes_on_exception(self):
        """Test that connect() closes the session even when the body raises."""
        from alancoin import connect

        self._mock_gateway_lifecycle()

        with pytest.raises(RuntimeError, match="boom"):
            with connect(self.BASE_URL, api_key="ak_test", budget="5.00") as gw:
                raise RuntimeError("boom")

        # Session should still be closed (POST + DELETE)
        assert len(responses.calls) == 2
        assert responses.calls[1].request.method == "DELETE"

    @responses.activate
    def test_connect_passes_parameters(self):
        """Test that connect() forwards max_per_call and allowed_services."""
        from alancoin import connect

        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            match=[
                matchers.json_params_matcher({
                    "maxTotal": "10.00",
                    "maxPerRequest": "2.00",
                    "expiresInSecs": 7200,
                    "allowedTypes": ["translation"],
                })
            ],
            json={
                "session": {
                    "id": "gw_params",
                    "token": "gw_params",
                    "maxTotal": "10.00",
                    "status": "active",
                }
            },
            status=201,
        )
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_params",
            json={"session": {"id": "gw_params", "status": "closed"}},
            status=200,
        )

        with connect(
            self.BASE_URL,
            api_key="ak_test",
            budget="10.00",
            max_per_call="2.00",
            expires_in="2h",
            allowed_services=["translation"],
        ) as gw:
            assert gw.session_id == "gw_params"

    @responses.activate
    def test_spend_returns_service_result(self):
        """Test that spend() does a one-shot call and returns the result."""
        from alancoin import spend

        self._mock_gateway_lifecycle(session_id="gw_spend")
        self._mock_proxy_call(output="translated text")

        result = spend(
            self.BASE_URL,
            api_key="ak_test",
            service_type="translation",
            budget="5.00",
            text="hello",
            target="es",
        )

        assert result["output"] == "translated text"
        # POST create + POST proxy + DELETE close
        assert len(responses.calls) == 3


# -----------------------------------------------------------------------------
# __getattr__ deprecation warnings
# -----------------------------------------------------------------------------

class TestDeprecationWarnings:
    """Test that legacy top-level imports emit DeprecationWarning."""

    def test_legacy_alancoin_import_warns(self):
        """Test that `from alancoin import Alancoin` emits DeprecationWarning."""
        with warnings.catch_warnings(record=True) as w:
            warnings.simplefilter("always")
            import importlib
            import alancoin
            importlib.reload(alancoin)
            # Access via __getattr__
            _ = alancoin.Alancoin
            deprecation_warnings = [x for x in w if issubclass(x.category, DeprecationWarning)]
            assert len(deprecation_warnings) >= 1
            assert "alancoin.admin" in str(deprecation_warnings[-1].message)

    def test_legacy_model_import_warns(self):
        """Test that `alancoin.Agent` emits DeprecationWarning pointing to alancoin.models."""
        with warnings.catch_warnings(record=True) as w:
            warnings.simplefilter("always")
            import alancoin
            _ = alancoin.Agent
            deprecation_warnings = [x for x in w if issubclass(x.category, DeprecationWarning)]
            assert any("alancoin.models" in str(dw.message) for dw in deprecation_warnings)

    def test_legacy_session_key_import_warns(self):
        """Test that `alancoin.SessionKeyManager` warns pointing to alancoin.session_keys."""
        with warnings.catch_warnings(record=True) as w:
            warnings.simplefilter("always")
            import alancoin
            _ = alancoin.SessionKeyManager
            deprecation_warnings = [x for x in w if issubclass(x.category, DeprecationWarning)]
            assert any("alancoin.session_keys" in str(dw.message) for dw in deprecation_warnings)

    def test_legacy_import_still_returns_correct_object(self):
        """Test that deprecated imports still return the right object."""
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", DeprecationWarning)
            import alancoin
            assert alancoin.Alancoin is Alancoin
            from alancoin.models import Agent
            assert alancoin.Agent is Agent

    def test_nonexistent_attribute_raises(self):
        """Test that accessing a truly missing attribute raises AttributeError."""
        import alancoin
        with pytest.raises(AttributeError, match="no attribute"):
            _ = alancoin.NoSuchThing

    def test_primary_exports_do_not_warn(self):
        """Test that the 6 primary exports do not trigger warnings."""
        with warnings.catch_warnings(record=True) as w:
            warnings.simplefilter("always")
            from alancoin import connect, spend, Budget, GatewaySession, AlancoinError, PolicyDeniedError
            deprecation_warnings = [x for x in w if issubclass(x.category, DeprecationWarning)]
            assert len(deprecation_warnings) == 0
