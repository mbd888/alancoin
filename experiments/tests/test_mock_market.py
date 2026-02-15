"""Tests for MockMarket CBA enforcement, transactions, and discovery."""

import pytest
from harness.clients.mock_market import MockMarket, MockAgent, MockTransaction, ServiceType


class TestCreateAgent:
    def test_creates_buyer(self):
        market = MockMarket(seed=1)
        agent = market.create_agent("Alice", "buyer", balance=50.0)
        assert agent.role == "buyer"
        assert agent.balance == 50.0
        assert agent.id in market.agents

    def test_creates_seller(self):
        market = MockMarket(seed=1)
        agent = market.create_agent("Bob", "seller", balance=0.0)
        assert agent.role == "seller"
        assert agent.balance == 0.0

    def test_unique_ids(self):
        market = MockMarket(seed=1)
        a1 = market.create_agent("A", "buyer")
        a2 = market.create_agent("B", "buyer")
        assert a1.id != a2.id

    def test_custom_limits(self):
        market = MockMarket(seed=1)
        agent = market.create_agent(
            "Alice", "buyer",
            max_per_tx=2.0, max_per_day=20.0, max_total=200.0,
        )
        assert agent.max_per_tx == 2.0
        assert agent.max_per_day == 20.0
        assert agent.max_total == 200.0

    def test_generates_wallet_addresses(self):
        market = MockMarket(seed=1)
        agent = market.create_agent("Alice", "buyer")
        assert agent.address.startswith("0x")
        assert agent.session_key_address.startswith("0x")


class TestAddService:
    def test_adds_service(self):
        market = MockMarket(seed=1)
        seller = market.create_agent("Bob", "seller")
        svc = market.add_service(
            seller.id, ServiceType.INFERENCE, "Inference",
            "desc", price=0.50,
        )
        assert svc.id in market.services
        assert svc.seller_id == seller.id
        assert svc.price == 0.50

    def test_sets_reference_price(self):
        market = MockMarket(seed=1)
        seller = market.create_agent("Bob", "seller")
        svc = market.add_service(
            seller.id, ServiceType.EMBEDDING, "Embed",
            "desc", price=0.20,
        )
        assert svc.reference_price == 0.10  # ground truth for embedding

    def test_unknown_seller_raises(self):
        market = MockMarket(seed=1)
        with pytest.raises(ValueError, match="not found"):
            market.add_service(
                "nonexistent", ServiceType.INFERENCE, "X", "d", price=1.0,
            )


class TestDiscoverServices:
    def setup_method(self):
        self.market = MockMarket(seed=1)
        self.seller = self.market.create_agent("Bob", "seller")
        self.market.add_service(
            self.seller.id, ServiceType.INFERENCE,
            "Inference", "desc", price=0.50,
        )
        self.market.add_service(
            self.seller.id, ServiceType.TRANSLATION,
            "Translation", "desc", price=0.30,
        )
        self.market.add_service(
            self.seller.id, ServiceType.EMBEDDING,
            "Embedding", "desc", price=0.10,
        )

    def test_discover_all(self):
        services = self.market.discover_services()
        assert len(services) == 3

    def test_filter_by_type(self):
        services = self.market.discover_services(service_type=ServiceType.INFERENCE)
        assert len(services) == 1
        assert services[0].service_type == ServiceType.INFERENCE

    def test_filter_by_max_price(self):
        services = self.market.discover_services(max_price=0.35)
        assert len(services) == 2  # translation (0.30) + embedding (0.10)

    def test_sorted_by_price_ascending(self):
        services = self.market.discover_services()
        prices = [s.price for s in services]
        assert prices == sorted(prices)

    def test_limit(self):
        services = self.market.discover_services(limit=1)
        assert len(services) == 1

    def test_inactive_services_excluded(self):
        # Deactivate one service
        svc_id = list(self.market.services.keys())[0]
        self.market.services[svc_id].active = False
        services = self.market.discover_services()
        assert len(services) == 2


class TestCBAConstraints:
    """Core CBA enforcement tests — the claim the paper rests on."""

    def test_per_tx_limit_rejects(self):
        market = MockMarket(seed=1, cba_enabled=True)
        buyer = market.create_agent("Alice", "buyer", max_per_tx=1.0)
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=1.50)
        assert tx.status == "rejected"
        assert "max_per_tx" in tx.rejection_reason

    def test_per_tx_limit_allows_within(self):
        market = MockMarket(seed=1, cba_enabled=True)
        buyer = market.create_agent("Alice", "buyer", max_per_tx=2.0)
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=1.50)
        assert tx.status == "accepted"

    def test_daily_limit_rejects(self):
        market = MockMarket(seed=1, cba_enabled=True)
        buyer = market.create_agent(
            "Alice", "buyer", max_per_tx=5.0, max_per_day=3.0,
        )
        seller = market.create_agent("Bob", "seller")
        market.transact(buyer.id, seller.id, amount=2.0)  # spent_today = 2
        tx = market.transact(buyer.id, seller.id, amount=2.0)  # would be 4 > 3
        assert tx.status == "rejected"
        assert "daily limit" in tx.rejection_reason

    def test_total_limit_rejects(self):
        market = MockMarket(seed=1, cba_enabled=True)
        buyer = market.create_agent(
            "Alice", "buyer",
            max_per_tx=10.0, max_per_day=10.0, max_total=5.0,
        )
        seller = market.create_agent("Bob", "seller")
        market.transact(buyer.id, seller.id, amount=3.0)
        tx = market.transact(buyer.id, seller.id, amount=3.0)  # total 6 > 5
        assert tx.status == "rejected"
        assert "total limit" in tx.rejection_reason

    def test_total_limit_zero_means_no_limit(self):
        market = MockMarket(seed=1, cba_enabled=True)
        buyer = market.create_agent(
            "Alice", "buyer",
            max_per_tx=999.0, max_per_day=999.0, max_total=0,
            balance=1000.0,
        )
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=500.0)
        assert tx.status == "accepted"

    def test_cba_disabled_allows_all(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", max_per_tx=1.0)
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=50.0)
        assert tx.status == "accepted"

    def test_insufficient_balance_rejects(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=1.0)
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=2.0)
        assert tx.status == "rejected"
        assert "insufficient balance" in tx.rejection_reason


class TestTransact:
    def test_updates_balances(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=10.0)
        seller = market.create_agent("Bob", "seller", balance=0.0)
        market.transact(buyer.id, seller.id, amount=3.0)
        assert buyer.balance == 7.0
        assert seller.balance == 3.0

    def test_tracks_spending(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=10.0)
        seller = market.create_agent("Bob", "seller")
        market.transact(buyer.id, seller.id, amount=2.0)
        assert buyer.spent_today == 2.0
        assert buyer.total_spent == 2.0

    def test_records_transaction(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=10.0)
        seller = market.create_agent("Bob", "seller")
        tx = market.transact(buyer.id, seller.id, amount=2.0)
        assert tx in market.transactions
        assert tx in buyer.transactions

    def test_unknown_sender_raises(self):
        market = MockMarket(seed=1)
        seller = market.create_agent("Bob", "seller")
        with pytest.raises(ValueError, match="Sender"):
            market.transact("nonexistent", seller.id, amount=1.0)

    def test_unknown_recipient_raises(self):
        market = MockMarket(seed=1)
        buyer = market.create_agent("Alice", "buyer")
        with pytest.raises(ValueError, match="Recipient"):
            market.transact(buyer.id, "nonexistent", amount=1.0)

    def test_updates_reputation(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=10.0)
        seller = market.create_agent("Bob", "seller")
        initial_buyer_rep = buyer.reputation_score
        initial_seller_rep = seller.reputation_score
        market.transact(buyer.id, seller.id, amount=1.0)
        assert buyer.reputation_score > initial_buyer_rep
        assert seller.reputation_score > initial_seller_rep


class TestResetDailyUsage:
    def test_resets_spent_today(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=100.0)
        seller = market.create_agent("Bob", "seller")
        market.transact(buyer.id, seller.id, amount=5.0)
        assert buyer.spent_today == 5.0
        market.reset_daily_usage()
        assert buyer.spent_today == 0.0
        assert buyer.total_spent == 5.0  # total NOT reset


class TestMarketStats:
    def test_empty_market(self):
        market = MockMarket(seed=1)
        stats = market.get_market_stats()
        assert stats["num_agents"] == 0
        assert stats["total_volume"] == 0.0

    def test_with_transactions(self):
        market = MockMarket(seed=1, cba_enabled=False)
        buyer = market.create_agent("Alice", "buyer", balance=100.0)
        seller = market.create_agent("Bob", "seller")
        market.transact(buyer.id, seller.id, amount=5.0)
        market.transact(buyer.id, seller.id, amount=3.0)
        stats = market.get_market_stats()
        assert stats["accepted_transactions"] == 2
        assert stats["total_volume"] == 8.0


class TestDeliverySimulation:
    def test_successful_delivery(self):
        market = MockMarket(seed=42)
        seller = market.create_agent("Bob", "seller")
        svc = market.add_service(
            seller.id, ServiceType.INFERENCE, "Inference",
            "desc", price=0.50, reliability=1.0, quality_score=0.9,
        )
        result = market.simulate_delivery(svc.id, "tx_001")
        assert result["success"] is True

    def test_service_not_found(self):
        market = MockMarket(seed=1)
        result = market.simulate_delivery("nonexistent", "tx_001")
        assert result["success"] is False

    def test_unreliable_service_can_fail(self):
        market = MockMarket(seed=1)
        seller = market.create_agent("Bob", "seller")
        svc = market.add_service(
            seller.id, ServiceType.INFERENCE, "Bad Service",
            "desc", price=0.50, reliability=0.0,
        )
        result = market.simulate_delivery(svc.id, "tx_001")
        assert result["success"] is False


class TestReproducibility:
    def test_same_seed_same_results(self):
        m1 = MockMarket(seed=42)
        s1 = m1.create_agent("Bob", "seller")
        svc1 = m1.add_service(s1.id, ServiceType.INFERENCE, "I", "d", 0.5, reliability=0.8)
        r1 = m1.simulate_delivery(svc1.id, "tx")

        m2 = MockMarket(seed=42)
        s2 = m2.create_agent("Bob", "seller")
        svc2 = m2.add_service(s2.id, ServiceType.INFERENCE, "I", "d", 0.5, reliability=0.8)
        r2 = m2.simulate_delivery(svc2.id, "tx")

        assert r1["success"] == r2["success"]
        assert r1["quality"] == r2["quality"]
