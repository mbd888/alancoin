"""Tests for Alancoin Python SDK."""

import pytest
import responses
from responses import matchers

from alancoin.admin import Alancoin
from alancoin.models import Agent, Service, ServiceListing, NetworkStats, ServiceType
from alancoin.exceptions import (
    AgentNotFoundError,
    AgentExistsError,
    ValidationError,
    PaymentRequiredError,
)


# -----------------------------------------------------------------------------
# Client Tests (mocked HTTP)
# -----------------------------------------------------------------------------

class TestAlancoinClient:
    """Test Alancoin API client."""
    
    BASE_URL = "http://localhost:8080"
    
    @pytest.fixture
    def client(self):
        return Alancoin(base_url=self.BASE_URL)
    
    @responses.activate
    def test_register_agent(self, client):
        """Test agent registration."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/agents",
            json={
                "address": "0x1234567890123456789012345678901234567890",
                "name": "TestAgent",
                "description": "A test agent",
                "services": [],
                "stats": {"totalReceived": "0", "totalSent": "0"},
            },
            status=201,
        )
        
        result = client.register(
            address="0x1234567890123456789012345678901234567890",
            name="TestAgent",
            description="A test agent",
        )

        agent = result["agent"]
        assert agent.address == "0x1234567890123456789012345678901234567890"
        assert agent.name == "TestAgent"
        assert agent.description == "A test agent"
    
    @responses.activate
    def test_register_agent_already_exists(self, client):
        """Test registration of existing agent."""
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/agents",
            json={"error": "agent_exists", "message": "Agent already registered"},
            status=409,
        )
        
        with pytest.raises(AgentExistsError):
            client.register(
                address="0x1234567890123456789012345678901234567890",
                name="TestAgent",
            )
    
    @responses.activate
    def test_get_agent(self, client):
        """Test getting an agent."""
        address = "0x1234567890123456789012345678901234567890"
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/agents/{address}",
            json={
                "address": address,
                "name": "TestAgent",
                "services": [
                    {"id": "svc1", "type": "inference", "name": "GPT-4", "price": "0.001"}
                ],
                "stats": {"transactionCount": 10},
            },
            status=200,
        )
        
        agent = client.get_agent(address)
        
        assert agent.address == address
        assert len(agent.services) == 1
        assert agent.services[0].type == "inference"
        assert agent.stats.transaction_count == 10
    
    @responses.activate
    def test_get_agent_not_found(self, client):
        """Test getting non-existent agent."""
        address = "0x0000000000000000000000000000000000000000"
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/agents/{address}",
            json={"error": "not_found", "message": "Agent not found"},
            status=404,
        )
        
        with pytest.raises(AgentNotFoundError):
            client.get_agent(address)
    
    @responses.activate
    def test_discover_services(self, client):
        """Test service discovery."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/services",
            json={
                "services": [
                    {
                        "id": "svc1",
                        "type": "translation",
                        "name": "English to Spanish",
                        "price": "0.001",
                        "agentAddress": "0x1111111111111111111111111111111111111111",
                        "agentName": "TranslatorBot",
                    },
                    {
                        "id": "svc2",
                        "type": "translation",
                        "name": "English to French",
                        "price": "0.002",
                        "agentAddress": "0x2222222222222222222222222222222222222222",
                        "agentName": "FrenchBot",
                    },
                ],
                "count": 2,
            },
            status=200,
        )
        
        services = client.discover(service_type="translation")
        
        assert len(services) == 2
        assert services[0].type == "translation"
        assert services[0].agent_name == "TranslatorBot"
        assert services[1].price == "0.002"
    
    @responses.activate
    def test_discover_with_price_filter(self, client):
        """Test service discovery with price filter."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/services",
            match=[
                matchers.query_param_matcher({
                    "type": "inference",
                    "maxPrice": "0.01",
                    "limit": "100",
                    "offset": "0",
                })
            ],
            json={"services": [], "count": 0},
            status=200,
        )
        
        services = client.discover(service_type="inference", max_price="0.01")
        assert len(services) == 0
    
    @responses.activate
    def test_add_service(self, client):
        """Test adding a service."""
        address = "0x1234567890123456789012345678901234567890"
        responses.add(
            responses.POST,
            f"{self.BASE_URL}/v1/agents/{address}/services",
            json={
                "id": "new-service-id",
                "type": "inference",
                "name": "GPT-4 API",
                "price": "0.001",
                "active": True,
            },
            status=201,
        )
        
        service = client.add_service(
            agent_address=address,
            service_type="inference",
            name="GPT-4 API",
            price="0.001",
        )
        
        assert service.id == "new-service-id"
        assert service.type == "inference"
        assert service.price == "0.001"
    
    @responses.activate
    def test_network_stats(self, client):
        """Test getting network stats."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/v1/network/stats",
            json={
                "totalAgents": 1547,
                "totalServices": 3892,
                "totalTransactions": 28493,
                "totalVolume": "4523.50",
            },
            status=200,
        )
        
        stats = client.stats()
        
        assert stats.total_agents == 1547
        assert stats.total_services == 3892
        assert stats.total_transactions == 28493
        assert stats.total_volume == "4523.50"
    
    @responses.activate
    def test_health(self, client):
        """Test health check."""
        responses.add(
            responses.GET,
            f"{self.BASE_URL}/health",
            json={"status": "healthy", "version": "0.1.0"},
            status=200,
        )
        
        health = client.health()
        assert health["status"] == "healthy"
    

# -----------------------------------------------------------------------------
# Model Tests
# -----------------------------------------------------------------------------

class TestModels:
    """Test data models."""
    
    def test_agent_from_dict(self):
        data = {
            "address": "0x1234",
            "name": "TestAgent",
            "description": "Test",
            "isAutonomous": True,
            "services": [
                {"id": "1", "type": "inference", "name": "API", "price": "0.001"}
            ],
            "stats": {"transactionCount": 5, "successRate": 0.95},
        }
        
        agent = Agent.from_dict(data)
        
        assert agent.address == "0x1234"
        assert agent.is_autonomous is True
        assert len(agent.services) == 1
        assert agent.stats.transaction_count == 5
        assert agent.stats.success_rate == 0.95
    
    def test_service_listing_from_dict(self):
        data = {
            "id": "svc1",
            "type": "translation",
            "name": "Translate",
            "price": "0.001",
            "agentAddress": "0xabc",
            "agentName": "Bot",
        }
        
        listing = ServiceListing.from_dict(data)
        
        assert listing.agent_address == "0xabc"
        assert listing.agent_name == "Bot"
    
    def test_service_types(self):
        assert ServiceType.INFERENCE == "inference"
        assert ServiceType.TRANSLATION == "translation"
        assert "inference" in ServiceType.ALL
        assert len(ServiceType.ALL) == 11


# -----------------------------------------------------------------------------
# Integration Test (skipped by default)
# -----------------------------------------------------------------------------

@pytest.mark.skip(reason="Requires running server and funded wallet")
class TestIntegration:
    """Integration tests against real server."""
    
    def test_full_flow(self):
        """Test full registration and discovery flow."""
        client = Alancoin(base_url="http://localhost:8080")
        
        # Register
        agent = client.register(
            address="0x1234567890123456789012345678901234567890",
            name="IntegrationTestAgent",
        )
        
        # Add service
        service = client.add_service(
            agent_address=agent.address,
            service_type="inference",
            name="Test Service",
            price="0.001",
        )
        
        # Discover
        services = client.discover(service_type="inference")
        assert any(s.id == service.id for s in services)
        
        # Cleanup
        client.delete_agent(agent.address)
