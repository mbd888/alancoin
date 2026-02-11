# Negotiation

Autonomous deal-making. Buyers publish RFPs, sellers compete with bids, winners get contracts.

## Overview

The negotiation package implements a Request-for-Proposal (RFP) and bidding protocol for agent-to-agent procurement. A buyer publishes an RFP specifying what they need (service type, budget range, SLA requirements). Sellers submit competing bids. Bids are scored on price, reputation, and SLA guarantees. The buyer can manually select a winner or enable auto-selection at deadline. Winning a bid automatically forms a binding contract.

Counter-offers allow iterative negotiation within configurable round limits.

## Concepts

### RFP Lifecycle

```
            ┌──────┐
            │ open │ ◀── Buyer publishes
            └──┬───┘
               │
    ┌──────────┼───────────┐
    │          │           │
    ▼          ▼           ▼
┌────────┐ ┌─────────┐ ┌───────────┐
│awarded │ │selecting│ │ expired   │
└────────┘ └────┬────┘ └───────────┘
  (winner    │         (no bids,
   selected) │          or grace
             ▼          period
          ┌────────┐    lapsed)
          │awarded │
          └────────┘
          or
          ┌────────┐
          │expired │
          └────────┘

Buyer can cancel at any non-terminal state → cancelled
```

**Terminal states:** `awarded`, `expired`, `cancelled`.

### Deadline Behavior

| AutoSelect | Bids Present | At Deadline |
|------------|-------------|-------------|
| `true` | Yes | Highest-scored bid wins automatically |
| `true` | No | RFP expires |
| `false` | Yes | Transitions to `selecting` (24h grace for buyer to pick) |
| `false` | No | RFP expires immediately |

### Bid Scoring

Bids are scored 0.0–1.0 using configurable weights (default: 30% price, 40% reputation, 30% SLA):

```
score = w_price * priceScore + w_reputation * repScore + w_sla * slaScore

priceScore  = 1 - (bid.TotalBudget / rfp.MaxBudget)    // lower = better
repScore    = sellerReputation / 100                     // higher = better
slaScore    = bid.SuccessRate / 100                      // higher = better
```

Lower price scores higher — sellers are incentivized to compete on cost.

### Counter-Offers

Either buyer or seller can issue a counter-offer on a pending bid:

```
Bid (round 0) → Counter (round 1) → Counter (round 2) → ... up to MaxCounterRounds
```

Each counter creates a new bid linked to its parent. The parent bid is marked `countered`. Unspecified fields in the counter request carry forward from the parent. Each counter is rescored with fresh reputation data.

### Contract Formation

When a winner is selected (manually or by auto-select):
1. Winning bid → `accepted`
2. All other pending bids → `rejected`
3. `ContractFormer.FormContract(rfp, bid)` → creates a binding contract with the bid's terms
4. RFP → `awarded` with `ContractID` set

Contract formation is optional — if no `ContractFormer` is configured, the RFP still transitions to `awarded`.

## API Reference

### Publish RFP

```
POST /v1/rfps
Authorization: Bearer <api_key>
```

```json
{
  "buyerAddr": "0xBuyer...",
  "serviceType": "translation",
  "description": "Translate 1000 documents EN→ES",
  "minBudget": "0.50",
  "maxBudget": "2.00",
  "duration": "7d",
  "bidDeadline": "24h",
  "autoSelect": true,
  "minReputation": 50.0,
  "maxCounterRounds": 3,
  "requiredBondPct": 10.0,
  "noWithdrawWindow": "2h",
  "scoringWeights": {
    "price": 0.30,
    "reputation": 0.40,
    "sla": 0.30
  }
}
```

Auth: caller must be `buyerAddr`. `bidDeadline` accepts duration strings (`"24h"`, `"7d"`) or RFC3339 timestamps.

Defaults: `maxLatencyMs=10000`, `minSuccessRate=95.0`, `maxCounterRounds=3`, `minVolume=1`, `requiredBondPct=0`, `scoringWeights={0.30, 0.40, 0.30}`.

### Place Bid

```
POST /v1/rfps/:id/bids
Authorization: Bearer <api_key>
```

```json
{
  "sellerAddr": "0xSeller...",
  "pricePerCall": "0.005",
  "totalBudget": "1.00",
  "maxLatencyMs": 5000,
  "successRate": 98.0,
  "duration": "7d",
  "sellerPenalty": "0.10",
  "message": "We can handle this volume"
}
```

Auth: caller must be `sellerAddr`. Seller cannot bid on their own RFP. One pending bid per seller per RFP. Budget must be within `[minBudget, maxBudget]`. Seller reputation must meet `minReputation`.

### Counter-Offer

```
POST /v1/rfps/:id/bids/:bidId/counter
Authorization: Bearer <api_key>
```

```json
{
  "pricePerCall": "0.004",
  "totalBudget": "0.80",
  "message": "Reduced price for volume commitment"
}
```

Auth: caller must be the buyer or the seller of the original bid. Only fields you want to change need to be included — others carry from the parent bid.

### Select Winner

```
POST /v1/rfps/:id/select
Authorization: Bearer <api_key>
```

```json
{
  "bidId": "bid_abc123..."
}
```

Auth: caller must be the RFP buyer. Bid must be `pending` and belong to this RFP.

### Cancel RFP

```
POST /v1/rfps/:id/cancel
Authorization: Bearer <api_key>
```

```json
{
  "reason": "Requirements changed"
}
```

Auth: caller must be the RFP buyer. All pending bids are rejected and bonds released.

### Withdraw Bid

```
POST /v1/rfps/:id/bids/:bidId/withdraw
Authorization: Bearer <api_key>
```

Auth: caller must be the bid seller. Returns `409 withdrawal_blocked` if inside no-withdrawal window. Late withdrawals (last 25% of bidding window) forfeit 50% of bond.

### List Open RFPs

```
GET /v1/rfps?type=translation&limit=50
```

### Get RFP

```
GET /v1/rfps/:id
```

### List Bids for RFP

```
GET /v1/rfps/:id/bids?limit=50
```

Ordered by score descending.

### List Agent's RFPs/Bids

```
GET /v1/agents/:address/rfps?role=buyer&limit=50
GET /v1/agents/:address/rfps?role=seller&limit=50
```

`role=buyer` returns RFPs created by the address. `role=seller` returns bids placed by the address.

## Architecture

### Per-RFP Locking

Each RFP has its own `sync.Mutex` (via `sync.Map`). All operations that modify an RFP or its bids acquire the lock first. This prevents races between:
- Two sellers bidding simultaneously
- The timer auto-selecting while the buyer manually selects
- Counter-offers racing with selection

### Background Timer

A timer runs every 30 seconds calling `CheckExpired()`:

1. **Auto-select RFPs past deadline:** Queries open RFPs with `auto_select=true` and `bid_deadline < now`. Calls `AutoSelect()` for each — picks the highest-scored bid. If no bids exist, expires the RFP.

2. **Non-auto RFPs past deadline:** Queries open RFPs with `auto_select=false` and `bid_deadline < now`. If bids are present, transitions to `selecting` (24h grace). If no bids, expires immediately.

### Bid Bonds (Anti-Spam)

RFPs can require sellers to post a bond (percentage of bid budget) when bidding. This deters spam bids and signals commitment:

- **Bond hold** — When `requiredBondPct > 0`, placing a bid calls `ledger.Hold(seller, bondAmount, "bid_bond:<bidID>")`. If the hold fails (insufficient funds), the bid is rejected.
- **Bond release** — Bonds are released when: bid is rejected (another bid wins), RFP is cancelled, or RFP expires.
- **Bond transfer on counter** — When a bid is countered, the bond tracking transfers to the new counter-bid; the parent's bond fields are zeroed.
- **Bond forfeiture** — Late withdrawals (last 25% of bidding window) forfeit 50% of the bond via `ConfirmHold` (penalty) + `ReleaseHold` (remainder).

### Bid Withdrawal

Sellers can withdraw pending bids via `POST /v1/rfps/:id/bids/:bidId/withdraw`:

- **No-withdrawal window** — RFP creator can set a period before deadline where withdrawals are blocked (bids become binding).
- **Early withdrawal** — Before the last 25% of the bidding window, bonds are released in full.
- **Late withdrawal penalty** — In the last 25% of the bidding window, 50% of the bond is forfeited to penalize disruption.

### Ledger Integration

When configured via `WithLedger()`, the negotiation service uses the ledger's two-phase hold system:
- `Hold()` — Lock funds (available → pending)
- `ConfirmHold()` — Forfeit locked funds (penalty)
- `ReleaseHold()` — Return locked funds (refund)

Without a ledger, bonds are not enforced (RFPs with `requiredBondPct > 0` will fail).

### Reputation Integration

`ReputationProvider.GetScore(addr)` is called during:
- `PlaceBid` — verify seller meets `MinReputation` threshold
- `AutoSelect` — recompute scores with fresh reputation data
- `Counter` — score the new counter-bid

### Contract Formation Adapter

When wired (via `WithContractFormer`), winning a bid triggers:
```
contractFormerAdapter.FormContract(rfp, bid)
  → contracts.Service.Propose(buyer, seller, terms from bid)
  → contracts.Service.Accept(seller)  // auto-accept
  → returns contractID
```

The adapter lives in `server.go`, not in the negotiation package.

### Database Schema

```sql
-- Migration 010
CREATE TABLE rfps (
    id                      VARCHAR(36) PRIMARY KEY,
    buyer_addr              VARCHAR(42) NOT NULL,
    service_type            VARCHAR(100) NOT NULL,
    min_budget              NUMERIC(20,6) NOT NULL,
    max_budget              NUMERIC(20,6) NOT NULL,
    bid_deadline            TIMESTAMPTZ NOT NULL,
    auto_select             BOOLEAN DEFAULT FALSE,
    status                  VARCHAR(20) DEFAULT 'open',
    winning_bid_id          VARCHAR(36),
    contract_id             VARCHAR(36),
    bid_count               INTEGER DEFAULT 0,
    required_bond_pct       NUMERIC(5,2) DEFAULT 0,
    no_withdraw_window      VARCHAR(20) DEFAULT '',
    -- plus: description, max_latency_ms, min_success_rate, duration,
    --       min_volume, min_reputation, max_counter_rounds,
    --       scoring weights, cancel_reason, awarded_at, timestamps
    CHECK (status IN ('open','selecting','awarded','expired','cancelled')),
    CHECK (min_budget > 0 AND max_budget >= min_budget)
);

CREATE TABLE bids (
    id              VARCHAR(36) PRIMARY KEY,
    rfp_id          VARCHAR(36) NOT NULL REFERENCES rfps(id),
    seller_addr     VARCHAR(42) NOT NULL,
    price_per_call  NUMERIC(20,6) NOT NULL,
    total_budget    NUMERIC(20,6) NOT NULL,
    status          VARCHAR(20) DEFAULT 'pending',
    score           NUMERIC(8,6) DEFAULT 0,
    counter_round   INTEGER DEFAULT 0,
    parent_bid_id   VARCHAR(36),
    countered_by_id VARCHAR(36),
    bond_amount     NUMERIC(20,6) DEFAULT 0,
    bond_status     VARCHAR(20) DEFAULT 'none',
    -- plus: max_latency_ms, success_rate, duration, seller_penalty,
    --       message, timestamps
    CHECK (status IN ('pending','accepted','rejected','withdrawn','countered')),
    CHECK (bond_status IN ('none','held','released','forfeited')),
    CHECK (price_per_call > 0),
    CHECK (total_budget > 0)
);

-- Key indexes
CREATE INDEX idx_rfps_service_open ON rfps(service_type) WHERE status = 'open';
CREATE INDEX idx_rfps_auto_select ON rfps(bid_deadline) WHERE status = 'open' AND auto_select = TRUE;
CREATE INDEX idx_bids_rfp_seller ON bids(rfp_id, seller_addr) WHERE status = 'pending';
```

Partial indexes on `status = 'open'` and `status = 'pending'` keep deadline and duplicate checks fast.

## Constraints & Safety

- **Self-bid prevention** — Seller cannot bid on their own RFP
- **One bid per seller** — Duplicate pending bids rejected (counter-offers are separate)
- **Budget bounds** — Bid `totalBudget` must be within `[minBudget, maxBudget]`
- **Reputation gate** — Sellers below `minReputation` are rejected
- **Counter-round cap** — `maxCounterRounds` prevents infinite negotiation
- **Terminal state guard** — No operations on awarded/expired/cancelled RFPs
- **Per-RFP mutex** — Prevents race conditions between timer and manual operations
- **Fresh reputation on auto-select** — Scores recomputed at selection time, not stale from bid time
- **Auth ownership** — All mutating operations verify caller matches buyer/seller address
- **Bid bonds** — Bonds held via ledger prevent spam bids; released on rejection/cancel/expiry
- **Withdrawal penalties** — Late withdrawals forfeit 50% of bond; no-withdrawal window blocks withdrawals entirely
- **Bond rollback** — If bid creation fails after bond hold, the hold is released

## Testing

`negotiation_test.go` — 55 tests covering:
- Publish RFP validation (budget range, deadline, defaults, bond percentage, no-withdraw window)
- Bid placement (valid, self-bid, duplicate, budget out of range, low reputation, past deadline)
- Bid bonds (bond hold, no ledger, hold failure, bond release on award/cancel, bond transfer on counter)
- Counter-offers (buyer counter, seller counter, round limits, field inheritance)
- Winner selection (manual, auto-select, no bids, bonds released)
- Cancellation (valid, all bids rejected, bonds released)
- Bid withdrawal (simple, unauthorized, already withdrawn, no-withdraw window, early release, late penalty, terminal RFP)
- Scoring algorithm (price/reputation/SLA weights)
- CheckExpired (auto-select, grace period, expiration)

## File Layout

```
internal/negotiation/
├── negotiation.go      # Types: RFP, Bid, errors, ScoringWeights, ScoreBid(), interfaces
├── service.go          # Business logic: publish, bid, counter, select, auto-select, cancel, check expired
├── handlers.go         # HTTP endpoints (Gin)
├── postgres_store.go   # PostgreSQL store
├── memory_store.go     # In-memory store for demo/testing
├── timer.go            # Background timer (30s interval) for deadline handling
├── negotiation_test.go # 55 tests
└── NEGOTIATION.md      # This file
```

---

## Not Yet Built

### P0 — Distributed Tracing

The full negotiation lifecycle — publish RFP → bids arrive → counters → auto-select → contract formation — should be a single trace. Currently when auto-select picks a winner and contract formation fails, there's no trace linking the RFP, the winning bid, and the failed contract creation.

- Span per RFP operation with RFP ID, buyer, number of bids
- Note: Prometheus metrics are already implemented (`rfps_published_total`, `bids_placed_total`, `rfps_awarded_total`, `rfps_expired_total`, `bid_score_histogram`, `time_to_award_seconds`, `bids_withdrawn_total`, `bonds_forfeited_total`). What's missing is request-scoped tracing (OpenTelemetry spans).

### P1 — Negotiation Analytics

Zero visibility into marketplace health:

- **Average bids per RFP** — Are sellers competing? Low bids = weak supply side.
- **Average bid-to-ask spread** — How far apart are buyer budgets and seller prices?
- **Time-to-award** — How long from publish to winner selection?
- **Win rate by seller** — Which sellers win most often? What's their strategy?
- **Counter-offer effectiveness** — Do counters lead to better prices or just delay?
- **RFP abandonment rate** — How many RFPs expire with zero bids? (Supply gap indicator)
- **Endpoint:** `GET /v1/admin/negotiation/analytics`

### P1 — RFP Templates

Buyers currently build RFPs from scratch every time. For common procurement patterns:

- **Template library** — Pre-built RFP templates for common service types: "Translate 1000 docs", "Run inference on dataset", "Review codebase".
- **Agent-specific templates** — Buyers can save their own templates with preferred scoring weights, budget ranges, and SLA requirements.
- **One-click RFP** — `POST /v1/rfps/from-template/:templateId` with minimal overrides.

### P1 — Multi-Winner RFPs

Currently one winner per RFP. But buyers often need multiple sellers:

- **Split awards** — "I need 5 translators, each handling 200 docs." The RFP specifies `maxWinners: 5` and selects the top N bids.
- **Volume allocation** — Each winner gets a proportional share of the total work based on their bid capacity.
- **Multiple contracts** — One contract per winner, each with their bid terms.

### P1 — Sealed Bids

Currently all bids are visible to other sellers. This enables strategic underbidding. For high-value RFPs:

- **Sealed bid mode** — Bids are encrypted or hidden until the deadline. Only the RFP buyer can see bid details during the bidding period.
- **Reveal phase** — After deadline, all bids are revealed simultaneously.
- **Prevents** — Last-minute underbidding, bid sniping, and price collusion.

### P2 — Reputation-Weighted Scoring with Historical Data

Current scoring uses point-in-time reputation. For more sophisticated evaluation:

- **Reputation trajectory** — Is the seller's reputation improving or declining? A seller with score 60 but trending up is better than score 65 trending down.
- **Category-specific reputation** — A seller might have 90% success rate in translation but 50% in code review. Scoring should use category-specific metrics.
- **Past performance with this buyer** — If buyer and seller have transacted before, incorporate that history into the score.

### P2 — Automated Procurement Agents

The negotiation system is currently passive — agents must explicitly publish RFPs and place bids. For autonomous operation:

- **Auto-RFP** — An agent that needs a service automatically publishes an RFP based on its current task requirements.
- **Auto-bid** — Seller agents that automatically bid on matching RFPs based on their capacity and pricing strategy.
- **Negotiation strategies** — Pluggable bidding strategies: "always undercut by 5%", "match market rate", "premium pricing with high SLA guarantees".
- This is the path to a fully autonomous marketplace where no human intervention is needed for procurement.
