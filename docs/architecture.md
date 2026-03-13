# Architecture

## System Overview

```
                         ┌──────────────────────┐
                         │     Dashboard UI     │ Real-time WebSocket
                         │    localhost:8080/   │ Network stats, tx feed
                         └──────────┬───────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                               SUPERVISOR                              │
│      Velocity · New-agent · Baseline (ML) · Circular flow · Conc.     │
│      Every transaction checked before execution · Denial logging      │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                             GATEWAY PROXY                             │
│         Budget session ── discover ── pay ── forward ── settle        │
│         Retry on failure · Strategy selection · Escrow protection     │
│         Dry-run · Single-shot · Shadow mode · Reconciliation          │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌──────────────┬────────────────────┼────────────────────┬──────────────┐
│ Session Keys │       Ledger       │       Escrow       │   Streams    │
│  ECDSA + 5-  │    NUMERIC(20,6)   │  Lock/deliver/     │  Hold/tick/  │
│    level     │   Two-phase holds  │  confirm/dispute   │    settle    │
│  delegation  │  Serializable txns │  Auto-release      │  Auto-close  │
└──────────────┴────────────────────┼────────────────────┴──────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                          REGISTRY + REPUTATION                        │
│        Service discovery · Agent registration · Reputation scoring    │
│        TraceRank (PageRank on payment graph) · Signed attestations    │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                      TENANCY + POLICIES + AUTH                        │
│      Multi-tenant (free/starter/growth/enterprise) · API key mgmt     │
│    Spend policies (shadow mode) · Per-tenant rate limits · Take rate  │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                             INFRASTRUCTURE                            │
│   Webhooks (HMAC-signed) · Receipts (HMAC-SHA256) · Circuit breaker   │
│  Prometheus metrics · OTEL tracing · Health checks · Graceful drain   │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                                STORAGE                                │
│             PostgreSQL (production) or In-Memory (dev/demo)           │
│            43 migrations · Serializable isolation · CHECK constraints │
└───────────────────────────────────┬───────────────────────────────────┘
                                    │
┌───────────────────────────────────┴───────────────────────────────────┐
│                              BASE L2 (USDC)                           │
│   On-chain USDC · Deposit watcher · Gas sponsorship · Price oracle    │
└───────────────────────────────────────────────────────────────────────┘
```

Agents only deal in USDC. The platform sponsors ETH gas fees, converting the cost to a small USDC markup. No agent ever holds or manages ETH.

## Core Loop

1. **Buyer creates a session** with a budget (`max_total`, `max_per_call`, service restrictions). Funds are held.
2. **Buyer sends a proxy request** (service type + parameters). The gateway discovers matching services, selects one (by price, reputation, or value), pays via escrow, and forwards the request.
3. **Service responds**. On success, escrow confirms and funds transfer to the seller. On failure, the buyer can dispute.
4. **Session closes**. Unspent funds are released back to the buyer.

Every transaction feeds the reputation system. Agents with consistent delivery earn higher scores, which earn better placement in discovery, which earns more business.

## Subsystems

### Gateway Proxy
The primary interface for agent-to-agent payments. Manages budget sessions, service discovery, payment proxying, and settlement. Supports 4 discovery strategies: `cheapest`, `reputation`, `tracerank`, and `best_value`.

### Supervisor
A 5-rule behavioral safety net that evaluates every transaction: velocity limits, new-agent caps, ML anomaly detection, circular flow detection, and counterparty concentration. Denials are logged with feature vectors for ML training data export.

### Session Keys
ECDSA-signed bounded-autonomy keys with hierarchical delegation (up to 5 levels), key rotation with grace periods, and HMAC-chain delegation proofs for O(1) ancestor verification.

### Ledger
NUMERIC(20,6) precision with serializable isolation. Two-phase holds ensure funds move `available -> pending` before any transfer, preventing double-spend.

### Escrow
Lock/deliver/confirm/dispute lifecycle with auto-release timeouts. Multi-step escrow for atomic N-step pipelines. Arbitration with evidence submission and partial settlement.

### Streams
Streaming micropayments with hold/tick/settle pattern. The buyer holds funds at stream open; each tick deducts from the hold. Auto-close on stale streams.

### Reputation & TraceRank
Dual scoring system. Traditional reputation is a weighted composite of volume, activity, success rate, age, and diversity. TraceRank is a PageRank-inspired algorithm that runs on the payment graph, using payment flows as implicit endorsements.

### Flywheel
Measures and amplifies network effects through 5 health sub-scores: velocity, growth, density, effectiveness, and retention. Feeds an incentive engine that gives fee discounts and discovery boosts to higher-reputation agents.

### Multi-Tenancy
Isolated agent namespaces with per-tenant rate limits, configurable take rates, and plan-based access (free/starter/growth/enterprise). Per-tenant dashboard with billing, usage timeseries, and denial logs.
