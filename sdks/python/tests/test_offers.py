"""Tests for offer/marketplace methods in the Alancoin Python SDK."""

import pytest
import responses

from alancoin.client import Alancoin


BASE_URL = "http://localhost:8080"


@pytest.fixture
def client():
    return Alancoin(base_url=BASE_URL, api_key="ak_test")


class TestPostOffer:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/offers",
            json={
                "id": "off_new",
                "sellerAddr": "0xseller",
                "serviceType": "translation",
                "price": "1.500000",
                "capacity": 10,
                "status": "active",
            },
            status=201,
        )

        result = client.post_offer(
            service_type="translation",
            price="1.50",
            capacity=10,
            description="Translation service",
            endpoint="https://example.com/translate",
        )
        assert result["id"] == "off_new"
        assert result["serviceType"] == "translation"

        body = responses.calls[0].request.body
        assert b"translation" in body
        assert b"1.50" in body


class TestListOffers:
    @responses.activate
    def test_list_all(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/offers",
            json={"offers": [{"id": "off_1"}, {"id": "off_2"}]},
        )

        result = client.list_offers()
        assert len(result["offers"]) == 2

    @responses.activate
    def test_filter_by_type(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/offers",
            json={"offers": [{"id": "off_llm"}]},
        )

        result = client.list_offers(service_type="llm", limit=5)
        assert "type=llm" in responses.calls[0].request.url
        assert "limit=5" in responses.calls[0].request.url


class TestGetOffer:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/offers/off_123",
            json={"id": "off_123", "price": "2.000000"},
        )

        result = client.get_offer("off_123")
        assert result["id"] == "off_123"


class TestListMyOffers:
    @responses.activate
    def test_success(self):
        client = Alancoin(base_url=BASE_URL, api_key="ak_test", address="0xmyseller")
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/agents/0xmyseller/offers",
            json={"offers": [{"id": "off_mine"}]},
        )

        result = client.list_my_offers()
        assert len(result["offers"]) == 1


class TestCancelOffer:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/offers/off_cancel/cancel",
            json={"id": "off_cancel", "status": "cancelled"},
        )

        result = client.cancel_offer("off_cancel")
        assert result["status"] == "cancelled"


class TestClaimOffer:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/offers/off_claim/claim",
            json={"claim": {"id": "clm_new", "offerId": "off_claim", "status": "pending"}},
            status=201,
        )

        result = client.claim_offer("off_claim")
        assert result["claim"]["id"] == "clm_new"


class TestGetClaim:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/claims/clm_123",
            json={"id": "clm_123", "status": "pending"},
        )

        result = client.get_claim("clm_123")
        assert result["id"] == "clm_123"


class TestListClaims:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.GET,
            f"{BASE_URL}/v1/offers/off_claims/claims",
            json={"claims": [{"id": "clm_1"}, {"id": "clm_2"}]},
        )

        result = client.list_claims("off_claims")
        assert len(result["claims"]) == 2


class TestDeliverClaim:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/claims/clm_deliver/deliver",
            json={"id": "clm_deliver", "status": "delivered"},
        )

        result = client.deliver_claim("clm_deliver")
        assert result["status"] == "delivered"


class TestCompleteClaim:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/claims/clm_done/complete",
            json={"id": "clm_done", "status": "completed"},
        )

        result = client.complete_claim("clm_done")
        assert result["status"] == "completed"


class TestRefundClaim:
    @responses.activate
    def test_success(self, client):
        responses.add(
            responses.POST,
            f"{BASE_URL}/v1/claims/clm_refund/refund",
            json={"id": "clm_refund", "status": "refunded"},
        )

        result = client.refund_claim("clm_refund")
        assert result["status"] == "refunded"
