"""Tests for GatewaySession and gateway-related client methods."""

import pytest
import responses
from decimal import Decimal
from responses import matchers

from alancoin.admin import Alancoin
from alancoin import GatewaySession
from alancoin.exceptions import AlancoinError, NetworkError


class TestGatewayClientMethods:
    """Test the new gateway methods on Alancoin client."""

    BASE_URL = "http://localhost:8080"

    @pytest.fixture
    def client(self):
        return Alancoin(base_url=self.BASE_URL, api_key="test_key")

    @responses.activate
    def test_create_gateway_session_minimal(self, client):
        """Test creating a gateway session with minimal parameters."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_123",
                    "agentAddr": "0xbuyer",
                    "maxTotal": "5.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                    "token": "gw_123",
                }
            },
            status=201,
        )

        result = client.create_gateway_session(max_total="5.00")

        assert result["session"]["id"] == "gw_123"
        assert result["session"]["status"] == "active"

    @responses.activate
    def test_create_gateway_session_full_params(self, client):
        """Test creating a gateway session with all parameters."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            match=[
                matchers.json_params_matcher({
                    "maxTotal": "10.00",
                    "expiresInSecs": 7200,
                    "allowedTypes": ["translation", "inference"],
                    "maxPerRequest": "1.00",
                })
            ],
            json={
                "session": {
                    "id": "gw_456",
                    "agentAddr": "0xbuyer",
                    "maxTotal": "10.00",
                    "status": "active",
                }
            },
            status=201,
        )

        result = client.create_gateway_session(
            max_total="10.00",
            expires_in="2h",
            allowed_services=["translation", "inference"],
            max_per_tx="1.00",
        )

        assert result["session"]["id"] == "gw_456"

    @responses.activate
    def test_gateway_proxy_with_extra_headers(self, client):
        """Test that gateway_proxy passes extra_headers correctly."""
        # This is the key test - verify extra_headers are sent
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            match=[
                matchers.json_params_matcher({
                    "serviceType": "translation",
                    "params": {"text": "hello", "target": "es"},
                }),
                matchers.header_matcher({"X-Gateway-Token": "gw_token_123"}),
            ],
            json={
                "result": {
                    "response": {"output": "hola"},
                    "amountPaid": "0.500000",
                    "serviceUsed": "0xseller",
                    "serviceName": "translator",
                    "latencyMs": 42,
                    "retries": 0,
                },
                "totalSpent": "0.500000",
                "remaining": "9.500000",
                "requestCount": 1,
            },
            status=200,
        )

        result = client.gateway_proxy(
            token="gw_token_123",
            service_type="translation",
            text="hello",
            target="es",
        )

        assert result["result"]["response"]["output"] == "hola"
        assert result["result"]["amountPaid"] == "0.500000"

    @responses.activate
    def test_close_gateway_session(self, client):
        """Test closing a gateway session."""
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_789",
            json={
                "session": {
                    "id": "gw_789",
                    "status": "closed",
                    "totalSpent": "2.50",
                    "refundedAmount": "2.50",
                }
            },
            status=200,
        )

        result = client.close_gateway_session("gw_789")

        assert result["session"]["status"] == "closed"
        assert result["session"]["totalSpent"] == "2.50"

    @responses.activate
    def test_get_gateway_session(self, client):
        """Test getting gateway session status."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_abc",
            json={
                "session": {
                    "id": "gw_abc",
                    "agentAddr": "0xbuyer",
                    "maxTotal": "10.00",
                    "totalSpent": "3.50",
                    "status": "active",
                    "requestCount": 5,
                }
            },
            status=200,
        )

        result = client.get_gateway_session("gw_abc")

        assert result["session"]["totalSpent"] == "3.50"
        assert result["session"]["requestCount"] == 5

    @responses.activate
    def test_list_gateway_sessions(self, client):
        """Test listing gateway sessions."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/gateway/sessions",
            match=[matchers.query_param_matcher({"limit": "50"})],
            json={
                "sessions": [
                    {"id": "gw_1", "status": "active"},
                    {"id": "gw_2", "status": "closed"},
                ],
                "count": 2,
            },
            status=200,
        )

        result = client.list_gateway_sessions(limit=50)

        assert len(result["sessions"]) == 2
        assert result["count"] == 2

    @responses.activate
    def test_list_gateway_logs(self, client):
        """Test listing gateway request logs."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_def/logs",
            match=[matchers.query_param_matcher({"limit": "100"})],
            json={
                "logs": [
                    {
                        "id": "log_1",
                        "sessionId": "gw_def",
                        "serviceType": "translation",
                        "cost": "0.50",
                        "status": "success",
                    },
                    {
                        "id": "log_2",
                        "sessionId": "gw_def",
                        "serviceType": "inference",
                        "cost": "0.75",
                        "status": "success",
                    },
                ],
                "count": 2,
            },
            status=200,
        )

        result = client.list_gateway_logs("gw_def", limit=100)

        assert len(result["logs"]) == 2
        assert result["logs"][0]["cost"] == "0.50"


class TestExtraHeadersParameter:
    """Test the extra_headers parameter added to _request()."""

    BASE_URL = "http://localhost:8080"

    @pytest.fixture
    def client(self):
        return Alancoin(base_url=self.BASE_URL, api_key="test_key")

    @responses.activate
    def test_request_without_extra_headers_still_works(self, client):
        """Test that existing code without extra_headers still works."""
        # This tests backward compatibility - crucial!
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/agents/0x123",
            json={
                "address": "0x123",
                "name": "TestAgent",
                "services": [],
                "stats": {},
            },
            status=200,
        )

        # Should not raise, existing code path should work
        agent = client.get_agent("0x123")
        assert agent.address == "0x123"

    @responses.activate
    def test_request_with_extra_headers_merges_correctly(self, client):
        """Test that extra_headers are merged with session headers."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/test",
            match=[
                matchers.header_matcher({
                    "Authorization": "Bearer test_key",
                    "Content-Type": "application/json",
                    "X-Custom-Header": "custom_value",
                }),
            ],
            json={"ok": True},
            status=200,
        )

        # Internal _request call with extra_headers
        result = client._request(
            "POST",
            "/v1/test",
            json={"data": "test"},
            extra_headers={"X-Custom-Header": "custom_value"},
        )

        assert result["ok"] is True

    @responses.activate
    def test_extra_headers_do_not_override_base_headers(self, client):
        """Test that extra_headers don't accidentally override base session headers."""
        # This is a critical test - extra_headers should not be able to override
        # Authorization or Content-Type headers set in the session
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/test",
            match=[
                # Should still have the original Authorization header
                matchers.header_matcher({
                    "Authorization": "Bearer test_key",
                    "X-Extra": "value",
                }),
            ],
            json={"ok": True},
            status=200,
        )

        result = client._request(
            "GET",
            "/v1/test",
            extra_headers={"X-Extra": "value"},
        )

        assert result["ok"] is True


class TestGatewaySessionClass:
    """Test the GatewaySession context manager and methods."""

    BASE_URL = "http://localhost:8080"

    @pytest.fixture
    def client(self):
        return Alancoin(base_url=self.BASE_URL, api_key="test_key")

    @responses.activate
    def test_gateway_session_context_manager_lifecycle(self, client):
        """Test that GatewaySession opens on enter and closes on exit."""
        # Open session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_lifecycle",
                    "token": "gw_lifecycle",
                    "agentAddr": "0xbuyer",
                    "maxTotal": "5.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )

        # Close session
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_lifecycle",
            json={
                "session": {
                    "id": "gw_lifecycle",
                    "status": "closed",
                    "totalSpent": "0.000000",
                }
            },
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            assert gw.is_active is True
            assert gw.session_id == "gw_lifecycle"
            assert gw.total_spent == "0"
            assert gw.remaining == "5.00"

        # After exit, session should be closed
        assert gw.is_active is False

    @responses.activate
    def test_gateway_session_call_method(self, client):
        """Test GatewaySession.call() for making proxy requests."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_call",
                    "token": "gw_call",
                    "maxTotal": "5.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )

        # Proxy call — server returns wrapped format
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "translated text"},
                    "amountPaid": "0.500000",
                    "serviceUsed": "0xseller",
                    "serviceName": "translator",
                    "latencyMs": 42,
                    "retries": 0,
                },
                "totalSpent": "0.500000",
                "remaining": "4.500000",
                "requestCount": 1,
            },
            status=200,
        )

        # Close session
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_call",
            json={"session": {"id": "gw_call", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            result = gw.call("translation", text="hello", target="es")

            assert result["output"] == "translated text"
            assert gw.total_spent == "0.500000"
            assert gw.remaining == "4.500000"
            assert gw.request_count == 1

    @responses.activate
    def test_gateway_session_tracks_spending(self, client):
        """Test that GatewaySession tracks total_spent correctly."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_track",
                    "token": "gw_track",
                    "maxTotal": "10.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )

        # First call
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "result1"},
                    "amountPaid": "1.500000",
                    "serviceUsed": "0xseller",
                },
                "totalSpent": "1.500000",
                "remaining": "8.500000",
                "requestCount": 1,
            },
            status=200,
        )

        # Second call
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "result2"},
                    "amountPaid": "2.250000",
                    "serviceUsed": "0xseller",
                },
                "totalSpent": "3.750000",
                "remaining": "6.250000",
                "requestCount": 2,
            },
            status=200,
        )

        # Close
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_track",
            json={"session": {"id": "gw_track", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="10.00") as gw:
            gw.call("translation", text="hello")
            assert gw.total_spent == "1.500000"
            assert gw.remaining == "8.500000"
            assert gw.request_count == 1

            gw.call("inference", text="analyze")
            assert gw.total_spent == "3.750000"
            assert gw.remaining == "6.250000"
            assert gw.request_count == 2

    @responses.activate
    def test_gateway_session_call_when_inactive_raises(self, client):
        """Test that calling on inactive session raises error."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_inactive",
                    "token": "gw_inactive",
                    "maxTotal": "5.00",
                    "status": "active",
                }
            },
            status=201,
        )

        # Close
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_inactive",
            json={"session": {"id": "gw_inactive", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            pass  # Just close it

        # Try to call after exit
        with pytest.raises(AlancoinError, match="not active"):
            gw.call("translation", text="hello")

    @responses.activate
    def test_gateway_session_close_when_inactive_raises(self, client):
        """Test that closing inactive session raises error."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_close_twice",
                    "token": "gw_close_twice",
                    "maxTotal": "5.00",
                    "status": "active",
                }
            },
            status=201,
        )

        # First close
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_close_twice",
            json={"session": {"id": "gw_close_twice", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            pass

        # Try to close again
        with pytest.raises(AlancoinError, match="not active"):
            gw.close()

    @responses.activate
    def test_gateway_session_logs(self, client):
        """Test GatewaySession.logs() method."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_logs",
                    "token": "gw_logs",
                    "maxTotal": "5.00",
                    "status": "active",
                }
            },
            status=201,
        )

        # Get logs
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_logs/logs",
            match=[matchers.query_param_matcher({"limit": "50"})],
            json={
                "logs": [
                    {"id": "log_1", "cost": "0.50"},
                    {"id": "log_2", "cost": "0.75"},
                ]
            },
            status=200,
        )

        # Close
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_logs",
            json={"session": {"id": "gw_logs", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="5.00") as gw:
            logs = gw.logs(limit=50)
            assert len(logs) == 2
            assert logs[0]["cost"] == "0.50"

    @responses.activate
    def test_gateway_session_refresh(self, client):
        """Test GatewaySession.refresh() updates local state."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_refresh",
                    "token": "gw_refresh",
                    "maxTotal": "10.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )

        # Proxy call
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "ok"},
                    "amountPaid": "2.500000",
                    "serviceUsed": "0xseller",
                },
                "totalSpent": "2.500000",
                "remaining": "7.500000",
                "requestCount": 1,
            },
            status=200,
        )

        # Refresh - simulates fetching updated state from server
        # (e.g., another concurrent client spent more)
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_refresh",
            json={
                "session": {
                    "id": "gw_refresh",
                    "maxTotal": "10.00",
                    "totalSpent": "5.00",
                    "status": "active",
                }
            },
            status=200,
        )

        # Close
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_refresh",
            json={"session": {"id": "gw_refresh", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="10.00") as gw:
            gw.call("translation", text="hello")
            assert gw.total_spent == "2.500000"

            # Refresh from server
            session_data = gw.refresh()
            assert gw.total_spent == "5.00"  # Updated from server
            assert session_data["totalSpent"] == "5.00"

    @responses.activate
    def test_gateway_session_exception_in_context_still_closes(self, client):
        """Test that exceptions in context manager still close the session."""
        # Create session
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_exception",
                    "token": "gw_exception",
                    "maxTotal": "5.00",
                    "status": "active",
                }
            },
            status=201,
        )

        # Close session (should be called even with exception)
        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_exception",
            json={"session": {"id": "gw_exception", "status": "closed"}},
            status=200,
        )

        with pytest.raises(ValueError, match="test error"):
            with client.gateway(max_total="5.00") as gw:
                assert gw.is_active is True
                raise ValueError("test error")

        # Verify session was still closed (check that DELETE was called)
        assert len(responses.calls) == 2  # POST create + DELETE close

    @responses.activate
    def test_gateway_convenience_method_parameters(self, client):
        """Test that client.gateway() passes all parameters correctly."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            match=[
                matchers.json_params_matcher({
                    "maxTotal": "15.00",
                    "expiresInSecs": 10800,
                    "allowedTypes": ["translation"],
                    "maxPerRequest": "2.00",
                })
            ],
            json={
                "session": {
                    "id": "gw_params",
                    "token": "gw_params",
                    "maxTotal": "15.00",
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

        with client.gateway(
            max_total="15.00",
            max_per_tx="2.00",
            expires_in="3h",
            allowed_services=["translation"],
        ) as gw:
            assert gw.session_id == "gw_params"

    @responses.activate
    def test_gateway_session_properties(self, client):
        """Test GatewaySession property accessors."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/sessions",
            json={
                "session": {
                    "id": "gw_props",
                    "token": "gw_props",
                    "maxTotal": "20.00",
                    "totalSpent": "0.000000",
                    "status": "active",
                }
            },
            status=201,
        )

        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/gateway/proxy",
            json={
                "result": {
                    "response": {"output": "ok"},
                    "amountPaid": "3.500000",
                    "serviceUsed": "0xseller",
                },
                "totalSpent": "3.500000",
                "remaining": "16.500000",
                "requestCount": 1,
            },
            status=200,
        )

        responses.add(
            responses.DELETE,
            f"{self.BASE_URL}/v1/gateway/sessions/gw_props",
            json={"session": {"id": "gw_props", "status": "closed"}},
            status=200,
        )

        with client.gateway(max_total="20.00") as gw:
            # Initial state
            assert gw.session_id == "gw_props"
            assert gw.total_spent == "0"
            assert gw.remaining == "20.00"
            assert gw.request_count == 0
            assert gw.is_active is True

            # After a call
            gw.call("test", param="value")
            assert gw.total_spent == "3.500000"
            assert gw.remaining == "16.500000"
            assert gw.request_count == 1
