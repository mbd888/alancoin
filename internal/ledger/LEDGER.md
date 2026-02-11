# Ledger

The financial foundation. Every balance change in the platform goes through here.

## Overview

The ledger tracks agent balances using a three-partition model: **available** (spendable), **pending** (held for on-chain transfers), and **escrowed** (locked for buyer protection). All arithmetic uses PostgreSQL `NUMERIC(20,6)` — no floats, no string math. A fund conservation invariant is enforced: `totalIn - totalOut = available + pending + escrowed`.

Every operation records an `Entry` in the ledger history for auditability.

## Balance Model

```
┌─────────────────────────────────────────┐
│              Agent Balance              │
├──────────┬──────────┬───────────────────┤
│ Available│ Pending  │ Escrowed          │
│ (spend)  │ (holds)  │ (buyer protection)│
├──────────┴──────────┴───────────────────┤
│ Credit: limit / used (optional)         │
├─────────────────────────────────────────┤
│ Lifetime: total_in / total_out          │
└─────────────────────────────────────────┘
```

| Field | Purpose |
|-------|---------|
| `available` | Immediately spendable balance |
| `pending` | Reserved by Hold, waiting for on-chain confirmation |
| `escrowed` | Locked by escrow, released on delivery confirmation |
| `credit_limit` | Maximum borrowable amount (optional, set by admin) |
| `credit_used` | Currently drawn credit (`<= credit_limit`) |
| `total_in` | Lifetime cumulative deposits |
| `total_out` | Lifetime cumulative debits |

## Operations

### Basic

| Operation | Effect | Entry Type |
|-----------|--------|------------|
| `Credit(addr, amount)` | available += amount, total_in += amount | `deposit` |
| `Debit(addr, amount)` | available -= amount, total_out += amount | `spend` |
| `Refund(addr, amount)` | available += amount, total_in += amount | `refund` |
| `Withdraw(addr, amount)` | available -= amount, total_out += amount | `withdrawal` |

Credit (deposit) auto-repays outstanding credit draws: if `credit_used > 0`, the deposit first reduces `credit_used` before increasing `available`.

Debit is credit-aware: if `available < amount`, the gap is drawn from the credit line. The effective spendable balance is `available + (credit_limit - credit_used)`.

### Two-Phase Holds

Used by session keys and streams to safely reserve funds before an on-chain transfer:

```
Hold          available → pending       (reservation)
ConfirmHold   pending → total_out       (transfer succeeded)
ReleaseHold   pending → available       (transfer failed/cancelled)
```

**With credit draw:** If `available < holdAmount`, the gap is covered by the credit line. A `credit_draw_hold` entry tracks the credit portion. On `ReleaseHold`, only the non-credit portion returns to `available` and the credit draw is reversed. On `ConfirmHold`, the credit stays drawn (it was a real spend).

Example:
```
State: available=$3, credit_limit=$10, credit_used=$0

Hold($5):  available=$0, pending=$5, credit_used=$2
           ($3 from available + $2 from credit)

ReleaseHold($5):  available=$3, pending=$0, credit_used=$0
                  (only $3 returned to available, $2 credit reversed)
```

### Escrow

Used by the escrow package for buyer protection:

```
EscrowLock     buyer.available → buyer.escrowed
ReleaseEscrow  buyer.escrowed → seller.available (+ seller.total_in, buyer.total_out)
RefundEscrow   buyer.escrowed → buyer.available
```

Hold and escrow are independent — an agent can have funds in both `pending` and `escrowed` simultaneously.

## API Reference

### Get Balance

```
GET /v1/agents/:address/balance
```

Returns the full balance object including available, pending, escrowed, credit, and lifetime totals.

### Get Ledger History

```
GET /v1/agents/:address/ledger
```

Returns the 50 most recent ledger entries (deposits, spends, holds, escrow operations, etc.).

### Record Deposit

```
POST /v1/admin/deposits
```

```json
{
  "agentAddr": "0xAgent...",
  "amount": "100.00",
  "txHash": "0xabc..."
}
```

Idempotent — duplicate `txHash` returns `ErrDuplicateDeposit`. Used by on-chain watchers and admin tooling.

### Withdraw

```
POST /v1/agents/:address/withdraw
Authorization: Bearer <api_key>
```

```json
{
  "amount": "50.00"
}
```

Uses the two-phase hold pattern internally: Hold → on-chain transfer → ConfirmHold (or ReleaseHold on failure). If no on-chain executor is configured (demo mode), the hold is confirmed immediately.

## Architecture

### No Pre-Balance Checks

Debit, Hold, and EscrowLock do **not** read the balance before attempting the operation. Instead, they use conditional SQL `WHERE` guards that atomically check and update:

```sql
UPDATE agent_balances SET
    available = GREATEST(0, available - $1),
    total_out = total_out + $1
WHERE agent_address = $2
  AND available >= $1   -- guard: fails if insufficient
```

This eliminates TOCTOU races. The operation either succeeds atomically or returns `ErrInsufficientBalance`.

### Serializable Isolation

All PostgreSQL operations use `sql.LevelSerializable`. This is the strongest isolation level — concurrent transactions that would conflict are serialized by the database. Callers must handle serialization failures (retry).

### NUMERIC(20,6) Arithmetic

Migration `002_numeric_balances.sql` converts all monetary columns from VARCHAR to `NUMERIC(20,6)`:
- 20 total digits (~1 trillion dollar capacity)
- 6 decimal places (USDC precision)
- All arithmetic happens in PostgreSQL, not in Go
- Go passes amounts as strings and the DB casts to NUMERIC

The `internal/usdc` package provides `Parse(string) → *big.Int` and `Format(*big.Int) → string` for the few places where Go-side arithmetic is needed (primarily the memory store).

### CHECK Constraints

```sql
CHECK (available >= 0)
CHECK (pending >= 0)
CHECK (escrowed >= 0)
CHECK (credit_used <= credit_limit)
```

These are the last line of defense. Even if application logic has a bug, the database will reject operations that would create negative balances or exceed credit limits.

### Entry Types

Every operation records a typed entry for auditability:

| Type | Recorded By |
|------|-------------|
| `deposit` | Credit |
| `withdrawal` | Withdraw |
| `spend` | Debit, ConfirmHold |
| `refund` | Refund |
| `hold` | Hold |
| `release` | ReleaseHold |
| `credit_draw` | Debit (when credit used) |
| `credit_draw_hold` | Hold (when credit used, tracking entry) |
| `credit_reverse` | ReleaseHold (reverses credit draw) |
| `credit_limit_set` | SetCreditLimit |
| `credit_repay` | RepayCredit, auto-repay on deposit |
| `escrow_lock` | EscrowLock |
| `escrow_release` | ReleaseEscrow (buyer side) |
| `escrow_receive` | ReleaseEscrow (seller side) |
| `escrow_refund` | RefundEscrow |

### Who Depends on the Ledger

| Package | Operations Used |
|---------|----------------|
| Session keys | `Debit` (spend), `Hold/ConfirmHold/ReleaseHold` (transfers) |
| Escrow | `EscrowLock`, `ReleaseEscrow`, `RefundEscrow` |
| Streams | `Hold/ConfirmHold/ReleaseHold` (streaming micropayments) |
| Stakes | `EscrowLock/ReleaseEscrow` (revenue distribution), `Debit/Credit` (market trades) |
| Contracts | `EscrowLock/ReleaseEscrow/RefundEscrow` (SLA contracts) |
| Credit | `SetCreditLimit`, `UseCredit`, `RepayCredit` |
| Server | Wires up HTTP handlers, deposit webhook |

## Store Interface

```go
type Store interface {
    // Basic
    GetBalance(ctx, agentAddr) (*Balance, error)
    Credit(ctx, agentAddr, amount, txHash, description) error
    Debit(ctx, agentAddr, amount, reference, description) error
    Refund(ctx, agentAddr, amount, reference, description) error
    Withdraw(ctx, agentAddr, amount, txHash) error
    GetHistory(ctx, agentAddr, limit) ([]*Entry, error)
    HasDeposit(ctx, txHash) (bool, error)

    // Two-phase holds
    Hold(ctx, agentAddr, amount, reference) error
    ConfirmHold(ctx, agentAddr, amount, reference) error
    ReleaseHold(ctx, agentAddr, amount, reference) error

    // Escrow
    EscrowLock(ctx, agentAddr, amount, reference) error
    ReleaseEscrow(ctx, buyerAddr, sellerAddr, amount, reference) error
    RefundEscrow(ctx, agentAddr, amount, reference) error

    // Credit
    SetCreditLimit(ctx, agentAddr, limit) error
    UseCredit(ctx, agentAddr, amount) error
    RepayCredit(ctx, agentAddr, amount) error
    GetCreditInfo(ctx, agentAddr) (limit, used string, err error)
}
```

Two implementations: `PostgresStore` (production) and `MemoryStore` (demo/testing). Both enforce the same invariants, but PostgresStore relies on DB constraints while MemoryStore uses `math/big` arithmetic with mutex locking.

## Constraints & Safety

- **Fund conservation** — `totalIn - totalOut = available + pending + escrowed` (verified in tests)
- **No negative balances** — CHECK constraints at DB level on all three partitions
- **Serializable isolation** — Strongest PostgreSQL isolation level on all transactions
- **No TOCTOU** — Conditional WHERE guards instead of read-then-write patterns
- **Idempotent deposits** — Duplicate txHash returns error, prevents double-crediting
- **Credit bounded** — `credit_used <= credit_limit` enforced at DB level
- **All amounts as NUMERIC(20,6)** — No floating point anywhere in the money path

## Testing

- `ledger_test.go` — Unit tests: all operations, hold lifecycle, credit draws, escrow, fund conservation assertions
- `postgres_store_test.go` — Integration tests: concurrent deposits, concurrent debits with overdraft prevention, serialization failure handling

## File Layout

```
internal/ledger/
├── ledger.go            # Public API: Ledger struct wrapping Store interface
├── handlers.go          # HTTP endpoints (balance, ledger history, deposits, withdrawals)
├── postgres_store.go    # PostgreSQL implementation (serializable, CHECK constraints)
├── memory_store.go      # In-memory implementation (demo/testing)
├── ledger_test.go       # Unit tests
├── postgres_store_test.go  # PostgreSQL integration tests
└── LEDGER.md            # This file

migrations/
├── 001_initial_schema.sql      # Creates agent_balances, ledger_entries (original VARCHAR)
└── 002_numeric_balances.sql    # Converts everything to NUMERIC(20,6)
```

---

## Not Yet Built

### P0 — Event Sourcing (Critical for Funding)

The ledger currently does **direct state mutation** — `UPDATE agent_balances SET available = available - $1`. This works, but it means the current balance is the only source of truth. For a financial system handling agent-to-agent payments, this is a non-starter for auditors and enterprise buyers.

**What needs to exist:**

- **Append-only event log** — Every `Hold`, `ConfirmHold`, `Deposit`, `Spend`, `EscrowLock`, etc. becomes an immutable event written to an `events` table. The current `ledger_entries` table is close but is not the authoritative source — it's a side effect of the mutation, not the cause.
- **Projections** — Current balances (`agent_balances`) become a materialized projection of the event stream, not a directly mutated row. A `RebuildBalance(agentAddr)` function replays all events for an agent and reconstructs the balance from scratch.
- **Point-in-time queries** — "What was agent X's balance at 2025-03-15T14:00:00Z?" Currently impossible. With event sourcing: replay events up to that timestamp.
- **Reconciliation tooling** — A `ReconcileAll()` function that replays every agent's event stream, computes the expected balance, and compares against `agent_balances`. Flags discrepancies. This should run on a cron (daily) and expose a `/v1/admin/reconcile` endpoint.

**Schema sketch:**
```sql
CREATE TABLE ledger_events (
    id          BIGSERIAL PRIMARY KEY,
    agent_addr  VARCHAR(42) NOT NULL,
    event_type  VARCHAR(30) NOT NULL,    -- 'hold', 'confirm_hold', 'deposit', etc.
    amount      NUMERIC(20,6) NOT NULL,
    reference   VARCHAR(255),
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    -- NEVER updated or deleted
);

-- Projection table (materialized from events)
-- agent_balances becomes a cache, not the source of truth
```

**Why this matters:** Every fintech that handles real money (Stripe, Square, Plaid) uses event sourcing or double-entry with immutable journals. Auditors will ask "show me the complete history of how this balance was computed" and right now the answer is "read the entries table and trust that it matches." That's not good enough for SOC2 or financial regulation.

### P0 — Distributed Tracing (OpenTelemetry)

Zero tracing exists. Every ledger operation should emit spans:

- **Span per operation** — `ledger.Hold`, `ledger.ConfirmHold`, `ledger.Deposit`, etc. Each span includes `agent_addr`, `amount`, `reference`, and duration.
- **Trace propagation** — When session keys calls `Hold()`, the trace ID flows from the HTTP request → session key validation → ledger hold → DB query. Currently if a hold fails in production, there's no way to correlate it with the originating request.
- **Prometheus metrics** — Counters for `ledger_operations_total{type="hold|confirm|deposit|..."}`, histograms for `ledger_operation_duration_seconds`, gauges for `ledger_total_balance_available`, `ledger_total_balance_pending`, `ledger_total_balance_escrowed`.

This is the difference between "it works on my laptop" and "we can debug a failed $50k settlement in production at 3am."

### P0 — On-Chain Reconciliation

The ledger tracks balances internally, but there's no mechanism to verify that the platform's USDC balance on-chain matches the sum of all agent balances in the ledger. This is a fundamental financial integrity gap.

**What needs to exist:**

- `ReconcileOnChain(ctx) (match bool, platformBalance, ledgerTotal string, err error)` — Fetches the platform wallet's USDC balance from the chain, sums all `available + pending + escrowed` across all agents, and compares.
- Alerting when the difference exceeds a threshold (e.g., >$1).
- A `/v1/admin/reconcile/onchain` endpoint for manual checks.
- A cron job that runs this daily and logs results.

### P1 — Transaction Reversal / Dispute Mechanism

Currently there's no way to reverse a completed transaction. If an agent is charged incorrectly, the only option is a manual admin deposit. For enterprise:

- `Reverse(ctx, entryID, reason)` — Creates a compensating entry that undoes the original. The original entry is marked as reversed but never deleted (immutability).
- Reversals must be admin-authorized and logged in an audit trail.
- Ties into the escrow dispute system — when a dispute is upheld, the ledger needs to process the reversal automatically.

### P1 — Audit Log (Regulatory Compliance)

Every balance-changing operation needs a compliance-grade audit trail:

- **Who** initiated it (agent address, API key ID, admin ID)
- **What** changed (operation type, amounts, before/after balances)
- **When** (timestamp with timezone)
- **Why** (reference, description, linked escrow/session key/stream ID)
- **From where** (IP address, request ID)

This is separate from `ledger_entries` — the audit log includes the human/system actor, not just the financial effect. Required for SOC2 Type II and any financial regulation.

### P1 — Balance Alerts and Notifications

No alerting exists for balance events:

- Agent balance drops below configurable threshold → webhook/notification
- Large single transaction (>$X) → flag for review
- Credit utilization exceeds 90% → alert agent
- Hold timeout approaching → warn originating system
- Balance anomaly detection (sudden spike or drain vs. historical pattern)

### P2 — Multi-Currency Support

The ledger is hardcoded to USDC with 6 decimal places. For multi-chain and multi-token support:

- `agent_balances` needs a `currency` column (or separate balance rows per currency)
- Operations need to specify currency
- Exchange rate integration for cross-currency transfers
- `NUMERIC(20,6)` works for USDC but other tokens have different decimal places (ETH has 18, BTC has 8)

### P2 — Batch Operations

No batch API exists. For high-throughput scenarios:

- `BatchDebit(ctx, []DebitRequest)` — Process multiple debits atomically
- `BatchDeposit(ctx, []DepositRequest)` — Process multiple deposits in one DB transaction
- Settlement netting: instead of A→B $5 + B→A $3, compute net A→B $2 and execute once
- This reduces DB round-trips and gas costs by 10-100x at scale
