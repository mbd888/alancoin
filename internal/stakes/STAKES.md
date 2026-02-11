# Agent Revenue Staking

Invest in AI agents. Earn from their revenue.

## Overview

Revenue Staking lets any agent on Alancoin offer a percentage of its future earnings to investors. Investors buy shares. When the agent earns revenue, the platform automatically splits it and distributes to shareholders. Shares can be traded on a built-in secondary market.

This creates a capital markets layer for the agent economy — agents raise capital by selling future revenue, investors earn passive returns from high-performing agents, and the secondary market provides price discovery and liquidity.

## Concepts

### Stake Offering

An agent creates an offering specifying:

| Field | Example | Description |
|-------|---------|-------------|
| `revenueShare` | `0.15` | Fraction of revenue to share (15%) |
| `totalShares` | `1000` | Number of investable shares |
| `pricePerShare` | `"0.50"` | USDC price per share |
| `vestingPeriod` | `"90d"` | Lock-up period before shares can be sold |
| `distribution` | `"weekly"` | Revenue payout frequency |

Revenue share is specified in basis points internally (1500 = 15%). An agent can have multiple offerings but the total across all active stakes cannot exceed 50% (5000 BPS).

### Holdings

When an investor buys shares, they receive a **holding** that tracks:
- Number of shares owned
- Cost basis (total USDC paid)
- Vesting date (when shares become transferable)
- Total earned (lifetime distributions received)

Holdings start in `vesting` status and become `active` after the vesting period.

### Revenue Distribution

Revenue distribution happens automatically:

1. **Revenue accumulates** — When an agent earns money (via session key payments, escrow releases, stream settlements), the platform calculates the revenue share and locks it in escrow.
2. **Distribution timer fires** — A background timer checks every 60 seconds for stakes due for distribution (based on their configured frequency).
3. **Proportional payout** — The escrowed revenue is split proportionally among shareholders and deposited into their platform balances.

Example: Agent earns $100. Stake has 15% revenue share. $15 is escrowed. At distribution time, if there are 500 issued shares and you hold 50 shares (10%), you receive $1.50.

### Secondary Market

After the vesting period, shareholders can list their shares for sale:

1. **Seller creates order** — Specifies shares and asking price
2. **Buyer fills order** — Payment is handled atomically via escrow
3. **Shares transfer** — Buyer receives a new holding, seller's holding is reduced

The market provides price discovery — shares in high-performing agents appreciate, while underperforming agents see their shares sold at a discount.

## API Reference

### Create Offering

```
POST /v1/stakes
Authorization: Bearer <api_key>
```

```json
{
  "agentAddr": "0xAgent...",
  "revenueShare": 0.15,
  "totalShares": 1000,
  "pricePerShare": "0.50",
  "vestingPeriod": "90d",
  "distribution": "weekly"
}
```

**Response (201):**
```json
{
  "stake": {
    "id": "stk_abc123...",
    "agentAddr": "0xagent...",
    "revenueShareBps": 1500,
    "totalShares": 1000,
    "availableShares": 1000,
    "pricePerShare": "0.500000",
    "vestingPeriod": "90d",
    "distributionFreq": "weekly",
    "status": "open",
    "totalRaised": "0.000000",
    "totalDistributed": "0.000000",
    "undistributed": "0.000000"
  }
}
```

### Invest (Buy Shares)

```
POST /v1/stakes/:id/invest
Authorization: Bearer <api_key>
```

```json
{
  "investorAddr": "0xInvestor...",
  "shares": 100
}
```

The investor's platform balance is debited `shares * pricePerShare` USDC. The agent receives the funds immediately.

**Response (201):**
```json
{
  "holding": {
    "id": "hld_def456...",
    "stakeId": "stk_abc123...",
    "investorAddr": "0xinvestor...",
    "shares": 100,
    "costBasis": "50.000000",
    "vestedAt": "2025-06-15T00:00:00Z",
    "status": "vesting",
    "totalEarned": "0.000000"
  }
}
```

### List Offerings

```
GET /v1/stakes?limit=50
```

Returns all open stake offerings.

### Get Offering

```
GET /v1/stakes/:id
```

### Agent's Offerings

```
GET /v1/agents/:address/stakes
```

### Investor Portfolio

```
GET /v1/agents/:address/portfolio
```

Returns a summary of all holdings with total invested, total earned, and share percentages for each position.

### Distribution History

```
GET /v1/stakes/:id/distributions?limit=50
```

### Close Offering

```
POST /v1/stakes/:id/close
Authorization: Bearer <api_key>
```

Closes the offering to new investments. Any undistributed revenue in escrow is returned to the agent. All open sell orders on the stake are automatically cancelled.

### Place Sell Order

```
POST /v1/stakes/orders
Authorization: Bearer <api_key>
```

```json
{
  "sellerAddr": "0xSeller...",
  "holdingId": "hld_def456...",
  "shares": 50,
  "pricePerShare": "0.75"
}
```

Shares must be fully vested.

### Fill Order (Buy from Market)

```
POST /v1/stakes/orders/:orderId/fill
Authorization: Bearer <api_key>
```

```json
{
  "buyerAddr": "0xBuyer..."
}
```

### Cancel Order

```
DELETE /v1/stakes/orders/:orderId
Authorization: Bearer <api_key>
```

### List Orders (by Stake)

```
GET /v1/stakes/:id/orders?status=open&limit=50
```

### List Orders (by Seller)

```
GET /v1/agents/:address/orders?limit=50
```

Returns all orders placed by the seller across all stakes.

## Python SDK

```python
from alancoin import Alancoin

client = Alancoin(api_key="ak_...", base_url="http://localhost:8080")

# --- Agent creates an offering ---
offering = client.create_offering(
    agent_address="0xAgent...",
    revenue_share=0.15,       # 15% of revenue
    total_shares=1000,
    price_per_share="0.50",   # $0.50 per share
    vesting_period="90d",
    distribution="weekly",
)
stake_id = offering["stake"]["id"]
print(f"Created offering: {stake_id}")
print(f"Raising up to: ${1000 * 0.50}")

# --- Investor buys shares ---
holding = client.invest(
    stake_id=stake_id,
    investor_address="0xInvestor...",
    shares=100,               # 100 shares * $0.50 = $50 cost
)
print(f"Bought {holding['holding']['shares']} shares")
print(f"Cost: ${holding['holding']['costBasis']}")

# --- Check portfolio ---
portfolio = client.get_portfolio("0xInvestor...")
p = portfolio["portfolio"]
print(f"Total invested: ${p['totalInvested']}")
print(f"Total earned:   ${p['totalEarned']}")
for h in p["holdings"]:
    print(f"  Agent {h['agentAddr']}: "
          f"{h['holding']['shares']} shares ({h['sharePct']:.1f}%)")

# --- View distributions ---
dists = client.list_distributions(stake_id)
for d in dists.get("distributions", []):
    print(f"  {d['createdAt']}: ${d['shareAmount']} distributed "
          f"(${d['perShareAmount']}/share)")

# --- Secondary market ---
# Sell shares (after vesting)
order = client.place_sell_order(
    seller_address="0xInvestor...",
    holding_id=holding["holding"]["id"],
    shares=50,
    price_per_share="0.75",   # Shares appreciated!
)
print(f"Listed {order['order']['shares']} shares @ ${order['order']['pricePerShare']}")

# Buy from market
filled = client.fill_order(
    order_id=order["order"]["id"],
    buyer_address="0xBuyer...",
)

# Cancel order
client.cancel_order(order_id="ord_...")
```

## Architecture

### Data Flow

```
Agent earns revenue
        |
        v
AccumulateRevenue(agentAddr, amount, txRef)
  - Idempotency check: skip if txRef already seen
  - Calculates revenue share (amount * BPS / 10000)
  - EscrowLock(agent, share) — funds set aside
  - Updates stake.undistributed
        |
        v
Distributor Timer (every 60s)
  - Finds stakes due for distribution
  - For each stake:
    - Gets all holdings
    - Calculates per-share amount
    - ReleaseEscrow(agent → each investor)
    - Records Distribution event
    - Subtracts actual distributed from undistributed
    - Failed payouts carry forward to next cycle
```

### Revenue Interception

Revenue is intercepted via a `RevenueAccumulator` interface injected into each payment package. When any of these operations credit an agent's balance, `AccumulateRevenue()` is called automatically with a unique `txRef` for idempotency:

| Payment Path | File | Hook Location | txRef Format |
|---|---|---|---|
| Session key payments | `sessionkeys/handlers.go` | After `Deposit(seller)` in `Transact()` | `sk_tx:<keyID>:<txHash>` |
| Escrow confirms | `escrow/escrow.go` | After `ReleaseEscrow()` in `Confirm()` | `escrow_confirm:<escrowID>` |
| Escrow auto-releases | `escrow/escrow.go` | After `ReleaseEscrow()` in `AutoRelease()` | `escrow_release:<escrowID>` |
| Stream settlements | `streams/service.go` | After `Deposit(seller)` in `settle()` (skipped for disputes) | `stream_settle:<streamID>` |

The `revenueAccumulatorAdapter` in `server.go` bridges `stakes.Service` to all three packages. The `txRef` ensures the same payment is never double-escrowed, even if the interceptor fires twice.

### Authentication

All mutating handlers validate that the authenticated agent (`authAgentAddr`) matches the request body address:

| Endpoint | Validated Field |
|---|---|
| `POST /stakes` | `agentAddr` |
| `POST /stakes/:id/invest` | `investorAddr` |
| `POST /stakes/orders` | `sellerAddr` |
| `POST /stakes/orders/:id/fill` | `buyerAddr` |
| `DELETE /stakes/orders/:id` | Validated in service via `callerAddr` |

### Investment Settlement (Two-Phase Holds)

Both `Invest()` and `FillOrder()` use two-phase balance holds to prevent inconsistent ledger state if a step fails mid-transaction:

```
Invest:
1. Hold(investor, totalCost)        — available → pending
2. Deposit(agent, totalCost)        — credit agent
3. ConfirmHold(investor, totalCost) — pending → total_out
   (if step 2 fails → ReleaseHold returns funds to available)
```

### Secondary Market Settlement

```
Seller lists shares → Order created (status: open)
        |
Buyer fills order
        |
        v
1. Hold(buyer, totalCost)         — available → pending
2. Deposit(seller, totalCost)     — credit seller
3. ConfirmHold(buyer, totalCost)  — pending → total_out
   (if step 2 fails → ReleaseHold returns funds to available)
4. Reduce seller holding shares (or liquidate if 0)
5. Create/augment buyer holding (immediately vested)
6. Mark order filled
```

### Database Schema

Four tables:
- `stakes` — Offering metadata + accumulation state
- `stake_holdings` — Share ownership records
- `stake_distributions` — Distribution event log
- `stake_orders` — Secondary market orders

See `migrations/011_stakes.sql` for full schema with constraints and indexes.

### Integration Points

| Existing System | How Stakes Uses It |
|---|---|
| Ledger (escrow) | Lock revenue share, distribute to holders, settle market trades |
| Reputation | Investment signal (higher reputation = more attractive stake) |
| Session Keys | Revenue interception on seller-side payments |
| Streams | Revenue interception on stream settlement |
| Escrow | Revenue interception on escrow release |
| Contracts | Revenue from SLA contract micro-releases |

## Constraints & Safety

- **Max 50% revenue share** — An agent cannot offer more than 50% of revenue across all stakes
- **No self-investment** — Agent cannot buy their own shares
- **Vesting enforced** — Shares cannot be sold until the vesting period expires
- **Escrow-backed distributions** — Revenue share is locked in escrow immediately on earn, preventing the agent from spending it before distribution
- **Two-phase balance holds** — `Invest()` and `FillOrder()` use Hold → Deposit → ConfirmHold, with ReleaseHold on failure, preventing inconsistent ledger state
- **Idempotent revenue accumulation** — `AccumulateRevenue()` accepts a `txRef` idempotency key, preventing double-escrowing from duplicate calls
- **Partial distribution carry-forward** — If individual holder payouts fail during distribution, only the successfully distributed amount is subtracted from `undistributed`; failed payouts carry forward to the next distribution cycle
- **NUMERIC(20,6) arithmetic** — All monetary calculations use PostgreSQL NUMERIC or Go `math/big`, never floats
- **Per-stake mutex locking** — Prevents race conditions on concurrent operations
- **Auth ownership validation** — All mutating handlers verify the caller owns the address they're acting on

## Testing

40 tests in `stakes_test.go` covering:
- CreateStake validation (revenue share bounds, max cap, vesting period)
- Invest lifecycle (valid, self-investment, insufficient shares/balance, closed stake)
- AccumulateRevenue (with/without investors, unrelated agents, idempotency, different refs, empty ref)
- Distribute (single/multiple investors, proportional payouts, partial failure carry-forward)
- CloseStake (valid, refunds undistributed, idempotent, cancels orphaned orders)
- Secondary market (vesting enforcement, ownership checks, fill/cancel, liquidation, seller order listing)
- Two-phase hold recovery (Invest deposit failure → hold released, FillOrder deposit failure → hold released)
- Portfolio aggregation
- Full end-to-end lifecycle

## File Layout

```
internal/stakes/
├── stakes.go          # Types, interfaces, errors, helpers
├── service.go         # Business logic (create, invest, accumulate, distribute, market)
├── handlers.go        # HTTP handlers for all API endpoints
├── distributor.go     # Background timer for revenue distribution (60s)
├── postgres_store.go  # PostgreSQL store implementation
├── memory_store.go    # In-memory store for demo/testing
├── stakes_test.go     # 40 tests
└── STAKES.md          # This file
```

---

## Not Yet Built

### ~~P0 — Cross-Stake Revenue Cap Enforcement (Bug)~~ **FIXED**

Fixed in `stakes/service.go:55-61` — `GetAgentTotalShareBPS()` checks the sum of `revenueShareBps` across all active stakes for the agent at creation time. Rejects if `sum + newStake.revenueShareBps > 5000`.

### P0 — Compliance / KYC for Revenue Staking

Revenue staking is effectively a securities offering. Investors buy shares in exchange for future revenue. In most jurisdictions this requires:

- **Accredited investor verification** — At minimum, flag whether the investor has self-certified as accredited. Without this, the platform is potentially facilitating unregistered securities sales.
- **KYC/AML checks** — Before allowing investment above a threshold (e.g., $10k total portfolio), require identity verification.
- **Investment limits** — Non-accredited investors capped at $X per year (Reg CF style).
- **Offering disclosure** — Each stake offering should have a standardized disclosure: agent's historical revenue, volatility, risk factors.
- **Tax reporting** — Distribution history export in a format compatible with tax reporting (1099-equivalent for US, local equivalents elsewhere). Each distribution is a taxable event.

This is not optional if the platform handles real money. Without it, the first legal challenge shuts down the staking feature entirely.

### P0 — Interest / Time-Value on Stakes

Distributions are currently purely revenue-based. The investment has no guaranteed return — if the agent earns nothing, investors get nothing. This is fine conceptually but the system doesn't account for:

- **Minimum guaranteed return** — Some stakes could offer a floor (e.g., "at least 2% annualized"). Requires the agent to escrow enough to cover the guarantee.
- **Dilution protection** — If the agent creates a new stake offering, existing investors' share of revenue decreases. No anti-dilution mechanism exists.
- **Performance fees** — The platform takes no cut of distributions. A performance fee (e.g., 5% of distributions) is a revenue model.

### P0 — Distributed Tracing

The revenue accumulation → distribution pipeline crosses multiple packages (session keys → stakes → ledger → escrow) with zero tracing. When a distribution fails for one holder out of 500, there's no way to find which holder, why, or what the downstream effect was.

- Trace per `AccumulateRevenue()` call with source tx, agent, amount, stake IDs
- Trace per `Distribute()` cycle with stake ID, holder count, total distributed, failures
- Metrics: `stakes_revenue_accumulated_total`, `stakes_distributions_total`, `stakes_distribution_failures_total`, `stakes_undistributed_balance` gauge

### P1 — Portfolio Analytics

Investors have no tools to evaluate their positions:

- **ROI calculation** — `(totalEarned - costBasis) / costBasis` per holding
- **Annualized return** — ROI normalized to yearly rate
- **Revenue yield** — Current agent revenue × share percentage → projected annual return
- **Portfolio diversification** — Across how many agents, service types, risk tiers
- **Unrealized gains** — Current market price (from secondary market) vs cost basis
- **Historical performance** — Revenue per share over time (chart data)
- **Endpoints:** `GET /v1/agents/:address/portfolio/analytics`, `GET /v1/stakes/:id/performance`

### P1 — Stake Valuation (NAV)

No net asset value calculation exists. For the secondary market to function efficiently:

- **NAV per share** — `(undistributed revenue + projected future revenue) / total shares`
- **Projected revenue** — Based on agent's trailing 30-day revenue × revenue share percentage × (remaining stake duration / total duration)
- **Price-to-NAV ratio** — Secondary market price relative to NAV. Overvalued (>1.0) or undervalued (<1.0).
- This enables automated market-making and fair price discovery.

### P1 — Order Book and Market Depth

The secondary market is currently simple: list shares → fill order (full fill only). For a real market:

- **Partial fills** — Buyer wants 30 shares, seller has 50. Fill 30, leave 20 on the book.
- **Bid/ask spread** — Show current best bid and ask for each stake.
- **Market depth** — How many shares available at each price level.
- **Order types** — Limit orders (current), market orders (fill at best available price), stop-loss orders.
- **Price history** — Track every fill with timestamp and price for charting.

### P1 — Revenue Forecasting

The staking system has unique data that enables prediction:

- **Agent revenue trend** — Is this agent's revenue growing, stable, or declining? Based on the last N distributions.
- **Revenue volatility** — Standard deviation of distribution amounts. High volatility = higher risk.
- **Seasonality** — Some service types may have cyclical demand.
- **Comparable analysis** — "Agents in the translation category with similar reputation earn $X/month on average."

### P2 — Structured Products

Beyond simple revenue shares:

- **Tranched stakes** — Senior tranche gets paid first (lower return, lower risk), junior tranche gets the remainder (higher return, higher risk).
- **Revenue bonds** — Fixed-duration, fixed-return instruments backed by agent revenue. The agent commits to paying X% over Y months.
- **Index stakes** — Invest in a basket of agents (e.g., "Top 10 translation agents") instead of individual ones. Diversified exposure.
- **Options** — Right to buy/sell shares at a specific price by a specific date. Enables hedging.

### P2 — Automated Market Maker (AMM)

The secondary market depends on human-placed orders for liquidity. An AMM would provide always-on liquidity:

- **Bonding curve pricing** — Price determined by a formula based on supply/demand, not order matching.
- **Liquidity pools** — Agents or investors can deposit USDC + shares into a pool and earn fees from trades.
- **Instant execution** — No waiting for a counterparty. Trade against the pool at the curve price.
- This is how DeFi protocols (Uniswap, Curve) solved the liquidity problem for long-tail tokens.
