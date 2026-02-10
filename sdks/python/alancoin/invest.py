"""
Agent Revenue Staking — invest in AI agents and earn from their revenue.

Usage:

    from alancoin import Alancoin

    client = Alancoin(api_key="ak_...")

    # Agent creates an offering (15% of revenue, 1000 shares at $0.50 each)
    offering = client.create_offering(
        agent_address="0xAgent...",
        revenue_share=0.15,
        total_shares=1000,
        price_per_share="0.50",
        vesting_period="90d",
        distribution="weekly",
    )

    # Investor buys 100 shares
    holding = client.invest(
        stake_id=offering["stake"]["id"],
        investor_address="0xInvestor...",
        shares=100,
    )

    # Check portfolio
    portfolio = client.get_portfolio("0xInvestor...")

    # Sell shares on secondary market
    order = client.place_sell_order(
        seller_address="0xInvestor...",
        holding_id=holding["holding"]["id"],
        shares=50,
        price_per_share="0.75",  # shares appreciated
    )
"""

from dataclasses import dataclass
from typing import List, Optional


@dataclass
class StakeOffering:
    """A revenue-sharing offering by an agent."""
    id: str
    agent_addr: str
    revenue_share_bps: int
    total_shares: int
    available_shares: int
    price_per_share: str
    vesting_period: str
    distribution_freq: str
    status: str
    total_raised: str
    total_distributed: str
    undistributed: str

    @property
    def revenue_share_pct(self) -> float:
        """Revenue share as a percentage (e.g., 15.0 for 15%)."""
        return self.revenue_share_bps / 100.0

    @property
    def issued_shares(self) -> int:
        return self.total_shares - self.available_shares

    @classmethod
    def from_dict(cls, data: dict) -> "StakeOffering":
        return cls(
            id=data.get("id", ""),
            agent_addr=data.get("agentAddr", ""),
            revenue_share_bps=data.get("revenueShareBps", 0),
            total_shares=data.get("totalShares", 0),
            available_shares=data.get("availableShares", 0),
            price_per_share=data.get("pricePerShare", "0"),
            vesting_period=data.get("vestingPeriod", ""),
            distribution_freq=data.get("distributionFreq", ""),
            status=data.get("status", ""),
            total_raised=data.get("totalRaised", "0"),
            total_distributed=data.get("totalDistributed", "0"),
            undistributed=data.get("undistributed", "0"),
        )


@dataclass
class StakeHolding:
    """Shares owned by an investor in a stake offering."""
    id: str
    stake_id: str
    investor_addr: str
    shares: int
    cost_basis: str
    status: str
    total_earned: str

    @classmethod
    def from_dict(cls, data: dict) -> "StakeHolding":
        return cls(
            id=data.get("id", ""),
            stake_id=data.get("stakeId", ""),
            investor_addr=data.get("investorAddr", ""),
            shares=data.get("shares", 0),
            cost_basis=data.get("costBasis", "0"),
            status=data.get("status", ""),
            total_earned=data.get("totalEarned", "0"),
        )


@dataclass
class StakeOrder:
    """A sell order on the secondary market."""
    id: str
    stake_id: str
    holding_id: str
    seller_addr: str
    shares: int
    price_per_share: str
    status: str
    filled_shares: int
    buyer_addr: str

    @classmethod
    def from_dict(cls, data: dict) -> "StakeOrder":
        return cls(
            id=data.get("id", ""),
            stake_id=data.get("stakeId", ""),
            holding_id=data.get("holdingId", ""),
            seller_addr=data.get("sellerAddr", ""),
            shares=data.get("shares", 0),
            price_per_share=data.get("pricePerShare", "0"),
            status=data.get("status", ""),
            filled_shares=data.get("filledShares", 0),
            buyer_addr=data.get("buyerAddr", ""),
        )


class InvestMixin:
    """
    Mixin that adds revenue staking methods to the Alancoin client.

    Not used standalone — mixed into the Alancoin class.
    """

    # -----------------------------------------------------------------
    # Offerings
    # -----------------------------------------------------------------

    def create_offering(
        self,
        agent_address: str,
        revenue_share: float,
        total_shares: int,
        price_per_share: str,
        vesting_period: str = "90d",
        distribution: str = "weekly",
    ) -> dict:
        """
        Create a revenue-sharing stake offering.

        The agent offers a percentage of future revenue to investors.
        Investors buy shares and receive proportional distributions.

        Args:
            agent_address: Agent's wallet address (must be authenticated)
            revenue_share: Fraction of revenue to share (0.15 = 15%)
            total_shares: Number of shares to offer
            price_per_share: Price per share in USDC (e.g., "0.50")
            vesting_period: Lock-up period (e.g., "90d", "30d")
            distribution: Payout frequency ("daily", "weekly", "monthly")

        Returns:
            Created stake offering

        Example:
            offering = client.create_offering(
                agent_address=wallet.address,
                revenue_share=0.15,
                total_shares=1000,
                price_per_share="0.50",
            )
            print(f"Offering {offering['stake']['id']} created")
            print(f"Raising: ${float(offering['stake']['pricePerShare']) * 1000}")
        """
        return self._request(
            "POST",
            "/v1/stakes",
            json={
                "agentAddr": agent_address,
                "revenueShare": revenue_share,
                "totalShares": total_shares,
                "pricePerShare": price_per_share,
                "vestingPeriod": vesting_period,
                "distribution": distribution,
            },
        )

    def get_offering(self, stake_id: str) -> dict:
        """
        Get a stake offering by ID.

        Args:
            stake_id: The stake ID (starts with "stk_")

        Returns:
            Stake offering details
        """
        return self._request("GET", f"/v1/stakes/{stake_id}")

    def list_offerings(self, limit: int = 50) -> List[dict]:
        """
        List open stake offerings.

        Args:
            limit: Maximum offerings to return (default 50)

        Returns:
            List of open stake offerings
        """
        return self._request("GET", "/v1/stakes", params={"limit": limit})

    def list_agent_offerings(self, agent_address: str) -> dict:
        """
        List stake offerings created by an agent.

        Args:
            agent_address: Agent's wallet address

        Returns:
            List of the agent's offerings (open, paused, closed)
        """
        return self._request("GET", f"/v1/agents/{agent_address}/stakes")

    def close_offering(self, stake_id: str) -> dict:
        """
        Close a stake offering (no new investments accepted).

        Any undistributed revenue is returned to the agent.
        Existing shareholders continue to receive distributions
        until all shares are sold on the secondary market.

        Args:
            stake_id: The stake ID to close

        Returns:
            Updated stake with status 'closed'
        """
        return self._request("POST", f"/v1/stakes/{stake_id}/close")

    # -----------------------------------------------------------------
    # Investing
    # -----------------------------------------------------------------

    def invest(
        self,
        stake_id: str,
        investor_address: str,
        shares: int,
    ) -> dict:
        """
        Buy shares in a stake offering.

        Payment is automatically deducted from the investor's platform
        balance (totalCost = shares * pricePerShare).

        Args:
            stake_id: The stake ID to invest in
            investor_address: Investor's wallet address
            shares: Number of shares to buy

        Returns:
            Created holding with vesting details

        Example:
            holding = client.invest(
                stake_id="stk_abc123",
                investor_address=wallet.address,
                shares=100,
            )
            print(f"Invested ${holding['holding']['costBasis']}")
            print(f"Vests at: {holding['holding']['vestedAt']}")
        """
        return self._request(
            "POST",
            f"/v1/stakes/{stake_id}/invest",
            json={
                "investorAddr": investor_address,
                "shares": shares,
            },
        )

    # -----------------------------------------------------------------
    # Portfolio
    # -----------------------------------------------------------------

    def get_portfolio(self, investor_address: str) -> dict:
        """
        Get an investor's full portfolio summary.

        Returns all holdings with total invested, total earned,
        and share percentages.

        Args:
            investor_address: Investor's wallet address

        Returns:
            Portfolio summary with holdings

        Example:
            portfolio = client.get_portfolio(wallet.address)
            print(f"Total invested: ${portfolio['portfolio']['totalInvested']}")
            print(f"Total earned: ${portfolio['portfolio']['totalEarned']}")
            for h in portfolio['portfolio']['holdings']:
                print(f"  {h['agentAddr']}: {h['holding']['shares']} shares"
                      f" ({h['sharePct']:.1f}%)")
        """
        return self._request("GET", f"/v1/agents/{investor_address}/portfolio")

    def list_holdings(self, investor_address: str) -> dict:
        """
        List all holdings for an investor.

        Args:
            investor_address: Investor's wallet address

        Returns:
            List of holdings
        """
        return self._request("GET", f"/v1/agents/{investor_address}/holdings")

    def list_stake_holdings(self, stake_id: str) -> dict:
        """
        List all holders of a specific stake.

        Args:
            stake_id: The stake ID

        Returns:
            List of holdings for this stake
        """
        return self._request("GET", f"/v1/stakes/{stake_id}/holdings")

    # -----------------------------------------------------------------
    # Distributions
    # -----------------------------------------------------------------

    def list_distributions(self, stake_id: str, limit: int = 50) -> dict:
        """
        List revenue distributions for a stake.

        Args:
            stake_id: The stake ID
            limit: Maximum distributions to return

        Returns:
            List of distribution events with amounts per share
        """
        return self._request(
            "GET",
            f"/v1/stakes/{stake_id}/distributions",
            params={"limit": limit},
        )

    # -----------------------------------------------------------------
    # Secondary Market
    # -----------------------------------------------------------------

    def place_sell_order(
        self,
        seller_address: str,
        holding_id: str,
        shares: int,
        price_per_share: str,
    ) -> dict:
        """
        List shares for sale on the secondary market.

        Shares must be fully vested before they can be sold.

        Args:
            seller_address: Seller's wallet address
            holding_id: The holding to sell from
            shares: Number of shares to sell
            price_per_share: Asking price per share in USDC

        Returns:
            Created sell order

        Example:
            order = client.place_sell_order(
                seller_address=wallet.address,
                holding_id="hld_abc123",
                shares=50,
                price_per_share="0.75",
            )
            print(f"Listed {order['order']['shares']} shares at"
                  f" ${order['order']['pricePerShare']} each")
        """
        return self._request(
            "POST",
            "/v1/stakes/orders",
            json={
                "sellerAddr": seller_address,
                "holdingId": holding_id,
                "shares": shares,
                "pricePerShare": price_per_share,
            },
        )

    def fill_order(self, order_id: str, buyer_address: str) -> dict:
        """
        Buy shares from a sell order on the secondary market.

        Payment (shares * pricePerShare) is deducted from the buyer's
        platform balance and transferred to the seller.

        Args:
            order_id: The order ID to fill
            buyer_address: Buyer's wallet address

        Returns:
            Filled order with transaction details
        """
        return self._request(
            "POST",
            f"/v1/stakes/orders/{order_id}/fill",
            json={"buyerAddr": buyer_address},
        )

    def cancel_order(self, order_id: str) -> dict:
        """
        Cancel a sell order.

        Args:
            order_id: The order ID to cancel

        Returns:
            Cancelled order
        """
        return self._request("DELETE", f"/v1/stakes/orders/{order_id}")

    def list_orders(
        self,
        stake_id: str,
        status: str = "",
        limit: int = 50,
    ) -> dict:
        """
        List sell orders for a stake.

        Args:
            stake_id: The stake ID
            status: Filter by status ("open", "filled", "cancelled")
            limit: Maximum orders to return

        Returns:
            List of orders
        """
        params = {"limit": limit}
        if status:
            params["status"] = status
        return self._request(
            "GET",
            f"/v1/stakes/{stake_id}/orders",
            params=params,
        )
