# Escrow (Buyer Protection)

Lock funds until service delivery is confirmed. Automatic release on timeout.

## Overview

Escrow protects buyers by holding their funds in a locked state until the seller delivers. The buyer can then confirm (releasing funds to the seller) or dispute (refunding themselves). If neither happens, a background timer auto-releases to the seller after a configurable timeout (default 5 minutes).

The escrow package doesn't handle dispute resolution — disputes auto-refund the buyer immediately. This is a deliberate simplification: the buyer always wins disputes.

## Concepts

### Escrow Lifecycle

```
             ┌─────────┐
             │ pending  │
             └────┬─────┘
                  │
        ┌─────────┼──────────┐
        v         v          v
  ┌──────────┐  ┌─────────┐  ┌──────────┐
  │ delivered│  │ released│  │ refunded │
  └────┬─────┘  └─────────┘  └──────────┘
       │         (confirm)    (dispute)
       │
  ┌────┼──────────┐
  v               v
┌─────────┐  ┌──────────┐
│ released│  │ refunded │
└─────────┘  └──────────┘

Auto-release timer fires on pending/delivered → expired (same as released)
```

Terminal states: `released`, `refunded`, `expired`. Once terminal, no further operations are possible.

### Fund Flow

| Operation | Ledger Effect |
|-----------|---------------|
| Create | `EscrowLock(buyer)` — buyer's available → escrowed |
| Confirm | `ReleaseEscrow(buyer → seller)` — buyer's escrowed → seller's available |
| Dispute | `RefundEscrow(buyer)` — buyer's escrowed → buyer's available |
| Auto-release | Same as Confirm |

Funds are always conserved. The ledger's `escrowed` column holds locked amounts, and CHECK constraints at the DB level prevent it from going negative.

### Revenue Interception

On Confirm and AutoRelease, the escrow package calls `RevenueAccumulator.AccumulateRevenue(seller, amount)`. This is how the stakes system intercepts revenue for shareholder distribution. The call is fire-and-forget — failures are logged but don't block the escrow state transition.

## API Reference

### Create Escrow

```
POST /v1/escrow
Authorization: Bearer <api_key>
```

```json
{
  "buyerAddr": "0xBuyer...",
  "sellerAddr": "0xSeller...",
  "amount": "1.50",
  "serviceId": "svc_optional",
  "sessionKeyId": "sk_optional",
  "autoRelease": "10m"
}
```

Auth: caller must be `buyerAddr`. Buyer and seller must be different addresses.

`autoRelease` accepts Go duration strings (`"5m"`, `"1h"`, `"30s"`). Defaults to 5 minutes.

**Response (201):**
```json
{
  "escrow": {
    "id": "esc_abc123...",
    "buyerAddr": "0xbuyer...",
    "sellerAddr": "0xseller...",
    "amount": "1.500000",
    "status": "pending",
    "autoReleaseAt": "2025-03-15T12:10:00Z",
    "createdAt": "2025-03-15T12:05:00Z"
  }
}
```

### Mark Delivered

```
POST /v1/escrow/:id/deliver
Authorization: Bearer <api_key>
```

No body. Auth: caller must be seller. Only valid from `pending` status.

### Confirm (Release to Seller)

```
POST /v1/escrow/:id/confirm
Authorization: Bearer <api_key>
```

No body. Auth: caller must be buyer. Valid from `pending` or `delivered`.

### Dispute (Refund to Buyer)

```
POST /v1/escrow/:id/dispute
Authorization: Bearer <api_key>
```

```json
{
  "reason": "Service was not delivered"
}
```

Auth: caller must be buyer. Reason is required. The dispute auto-refunds immediately — there is no mediation step.

### Get Escrow

```
GET /v1/escrow/:id
```

### List Agent's Escrows

```
GET /v1/agents/:address/escrows?limit=50
```

Returns escrows where the address is buyer OR seller.

## Architecture

### Per-Escrow Locking

Each escrow ID has its own `sync.Mutex` (stored in a `sync.Map`). This prevents race conditions when Confirm and AutoRelease fire simultaneously on the same escrow. Operations on different escrows proceed concurrently.

The lock is acquired before reading from the store, so the state check and transition are atomic from the application's perspective.

### Rollback Strategy

The escrow package must coordinate ledger changes with store updates. If one succeeds but the other fails, compensation is needed:

| Scenario | Strategy |
|----------|----------|
| Create: `EscrowLock` succeeds, `store.Create` fails | Call `RefundEscrow` to reverse the lock |
| Confirm: `ReleaseEscrow` succeeds, `store.Update` fails | Retry store update once. If still fails, log CRITICAL (funds already moved, no inverse operation) |
| Dispute: `RefundEscrow` succeeds, `store.Update` fails | Re-lock funds via `EscrowLock` to compensate |

The CRITICAL log on confirm/auto-release store failure is the one edge case where the system can become inconsistent. In practice this requires the database to fail mid-transaction, which is rare.

### Auto-Release Timer

A background goroutine (`Timer`) checks every 30 seconds for expired escrows:

1. Queries `ListExpired(now, limit=100)` — returns non-terminal escrows past their `autoReleaseAt`
2. For each, calls `AutoRelease()` which re-reads under lock and releases to seller
3. Errors on individual escrows are logged and skipped (batch continues)

The timer is started in `server.go` and stopped on shutdown.

### Interfaces

The escrow package depends on four interfaces, all injected via the `Service` constructor:

| Interface | Provider | Purpose |
|-----------|----------|---------|
| `Store` | `PostgresStore` / `MemoryStore` | Persistence |
| `LedgerService` | Ledger adapter in `server.go` | Fund movements |
| `TransactionRecorder` | Registry adapter in `server.go` | Records tx for reputation |
| `RevenueAccumulator` | Stakes adapter in `server.go` | Revenue interception for staking |

`TransactionRecorder` and `RevenueAccumulator` are optional (nil-checked before use).

### Database Schema

```sql
-- Migration 003
CREATE TABLE escrows (
    id              VARCHAR(36) PRIMARY KEY,
    buyer_addr      VARCHAR(42) NOT NULL,
    seller_addr     VARCHAR(42) NOT NULL,
    amount          NUMERIC(20,6) NOT NULL,
    service_id      VARCHAR(255),
    session_key_id  VARCHAR(255),
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    auto_release_at TIMESTAMPTZ NOT NULL,
    delivered_at    TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    dispute_reason  TEXT,
    resolution      TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Partial index: only non-terminal escrows checked for auto-release
CREATE INDEX idx_escrow_auto_release ON escrows(auto_release_at)
    WHERE status IN ('pending', 'delivered');
```

The migration also adds `escrowed NUMERIC(20,6)` to `agent_balances` with a `CHECK (escrowed >= 0)` constraint.

## Constraints & Safety

- **Buyer ≠ seller** — Self-escrow is rejected at creation
- **Auth ownership** — Create/confirm/dispute require buyer; deliver requires seller
- **Terminal state guard** — No operations on released/refunded/expired escrows
- **Per-escrow mutex** — Concurrent operations on same escrow are serialized
- **DB-level CHECK** — `escrowed >= 0` constraint prevents overdraw
- **Fire-and-forget integrations** — Revenue accumulation and transaction recording never block escrow state transitions

## Testing

Tests across 4 files (~3700 lines):
- `escrow_test.go` — Unit tests: lifecycle, auth, concurrent confirm/auto-release races, ledger failure compensation
- `handlers_test.go` — HTTP tests: all endpoints, auth checks, error codes
- `postgres_store_test.go` — Integration tests with real PostgreSQL
- `integration_test.go` — End-to-end with real Ledger service, fund conservation assertions

## File Layout

```
internal/escrow/
├── escrow.go           # Types, Service struct, all state transitions
├── handlers.go         # HTTP endpoints (Gin)
├── memory_store.go     # In-memory Store for demo/testing
├── postgres_store.go   # PostgreSQL Store
├── timer.go            # Background auto-release ticker (30s interval)
├── escrow_test.go      # Unit tests
├── handlers_test.go    # HTTP handler tests
├── postgres_store_test.go  # PostgreSQL integration tests
├── integration_test.go # End-to-end tests with real ledger
└── ESCROW.md           # This file
```

---

## Not Yet Built

### P0 — Arbitration / Dispute Resolution

Currently disputes auto-refund the buyer with no recourse for the seller. The buyer always wins. This is a liability:

- **Arbitration service** — When both parties disagree, a third-party arbitrator (admin, DAO, or algorithmic) reviews evidence and decides the outcome. The escrow should support a `StatusArbitrating` state between dispute and resolution.
- **Evidence submission** — Both buyer and seller should be able to attach evidence (hashes, logs, screenshots) to a dispute. Currently `dispute_reason` is just a string.
- **Partial resolution** — "80% of the work was delivered, refund 20%." Currently it's all-or-nothing. `PartialRelease(ctx, escrowID, buyerRefundAmount, sellerReleaseAmount)` should split the escrowed funds.
- **Dispute window** — After delivery is marked, the buyer should have a configurable window (e.g., 24h) to dispute before auto-release. Currently auto-release fires on the original timeout regardless of delivery status.

**Schema additions needed:**
```sql
ALTER TABLE escrows ADD COLUMN dispute_evidence JSONB DEFAULT '[]';
ALTER TABLE escrows ADD COLUMN arbitrator_addr VARCHAR(42);
ALTER TABLE escrows ADD COLUMN arbitration_deadline TIMESTAMPTZ;
ALTER TABLE escrows ADD COLUMN partial_release_amount NUMERIC(20,6);
ALTER TABLE escrows ADD COLUMN partial_refund_amount NUMERIC(20,6);
```

### P0 — Reputation Impact from Disputes

Disputes currently have zero effect on seller reputation. A seller can fail to deliver, get disputed, lose the funds, and their reputation score doesn't change. This needs to be wired:

- `ReputationImpactor` interface — On dispute resolution, call `RecordDispute(sellerAddr, outcome, amount)` which feeds into the reputation scoring.
- Dispute rate should be a component of reputation score (e.g., >10% dispute rate = score penalty).
- Buyers who abuse disputes (>20% dispute rate) should also be flagged.

### P0 — Distributed Tracing (OpenTelemetry)

No tracing on any escrow operation. The full lifecycle — create → lock funds → deliver → confirm/dispute → settle — should be a single trace with child spans. When a `ConfirmHold` fails after `ReleaseEscrow` succeeds (the CRITICAL log case), the trace is the only way to debug it in production.

- Span per state transition with escrow ID, buyer, seller, amount
- Metrics: `escrow_created_total`, `escrow_confirmed_total`, `escrow_disputed_total`, `escrow_auto_released_total`, `escrow_duration_seconds` histogram

### P1 — Escrow Templates / Recurring Escrows

For ongoing service relationships (e.g., agent A always buys from agent B):

- **Escrow templates** — Pre-configured escrow parameters (amount, auto-release time, parties) that can be instantiated with one call.
- **Recurring escrows** — Auto-create a new escrow on a schedule (daily, weekly) for subscription-style agent relationships.
- **Linked escrows** — Chain escrows together for multi-step pipelines (summarize → translate → extract). If step 2 fails, refund step 3's escrow too.

### P1 — Conditional Release

Currently release is binary: buyer confirms or disputes. For more sophisticated service delivery:

- **Milestone-based release** — Split escrow into milestones. Each milestone releases a portion. `CreateEscrow(..., milestones: [{description: "Draft", amount: "0.50"}, {description: "Final", amount: "1.00"}])`.
- **Oracle-based release** — Integrate with external verification (e.g., an LLM quality scorer). If quality score > threshold, auto-release. If below, auto-dispute.
- **Time-locked partial release** — Release 50% immediately on delivery, hold 50% for a satisfaction period.

### P1 — Escrow Analytics

No visibility into escrow health:

- Average time to delivery per seller
- Dispute rate by seller, by service type, by amount range
- Auto-release rate (high rate means sellers aren't confirming delivery — process issue)
- Funds currently locked in escrow (platform-wide liquidity metric)
- Endpoint: `GET /v1/admin/escrow/analytics`

### P2 — Multi-Party Escrow

Current escrow is strictly buyer-seller. For pipelines involving multiple agents:

- **N-party escrow** — Lock funds once, release to multiple sellers as each completes their step.
- **Escrow splits** — Single escrow, multiple beneficiaries with pre-agreed split percentages.
- This ties into the pipeline API in the SDK — currently each pipeline step creates a separate escrow, which is gas-inefficient and doesn't handle cross-step failures gracefully.
