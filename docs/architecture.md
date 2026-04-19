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
│                         ENTERPRISE PLUGINS                            │
│   KYA Identity (W3C DID, EU AI Act) · FinOps Chargeback (budgets)    │
│   Dispute Arbitration (auto-resolve + arbiter) · Spend Forensics     │
│   Anomaly detection (6-signal, Welford stats, graduated alerts)      │
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
│            40 migrations · Serializable isolation · CHECK constraints │
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

### Coalition Escrow
Multi-agent escrow with oracle-judged quality tiers and Shapley-based payment splitting. A buyer locks a budget, defines coalition members and quality tiers, and an oracle evaluates each member's contribution. Settlement distributes funds proportionally based on the configured split strategy (equal, weighted, or Shapley value).

### Behavioral Contracts
Runtime enforcement of agent behavior during escrow execution, based on the Agent Behavioral Contracts (ABC) framework (arxiv:2602.22302). Contracts define preconditions (must be true before execution), invariants (must remain true during execution), and recovery actions (abort, degrade, or alert on violation). Contracts gate escrow settlement: payment releases only when the behavioral SLA is satisfied.

### Standing Offers (Marketplace)
Two-sided order book for agent services. Sellers post offers with service type, price, capacity, and conditions (minimum reputation, allowed buyers, minimum balance). Buyers claim offers atomically. Funds lock in escrow, capacity decrements. Supports deliver/complete/refund lifecycle with auto-expiry.

### Multi-Tenancy
Isolated agent namespaces with per-tenant rate limits, configurable take rates, and plan-based access (free/starter/growth/enterprise). Per-tenant dashboard with billing, usage timeseries, and denial logs.

### KYA (Know Your Agent) Identity
Signed identity certificates for AI agents combining organizational binding, permission scope, and TraceRank reputation into a W3C DID-compatible credential. Certificates are HMAC-signed, auto-revoked when session keys expire, and exportable as EU AI Act Article 12 technical documentation. Trust tiers (AAA through D) are computed from transaction history, dispute rate, and account age.

### FinOps Chargeback Engine
Per-department agent cost attribution with real-time budget enforcement. Every payment event can be tagged with a cost center, department, and project code. Monthly budget envelopes are enforced at the ledger level. Spend events are rejected when a cost center exhausts its allocation. Generates CFO-ready chargeback reports with per-department breakdowns, top-service analysis, and budget utilization percentages.

### Dispute Arbitration
Programmatic dispute resolution for agent escrows. Two resolution paths: (1) auto-resolve by comparing service output against behavioral contract specifications, and (2) human/agent arbiter assignment with evidence submission. Outcomes trigger financial execution via existing escrow primitives (refund, release, or percentage-based split). Fee: 2% of disputed amount. Loser receives reputation penalty via TraceRank.

### Spend Forensics
Behavioral anomaly detection engine. Establishes per-agent payment baselines using Welford's online algorithm for running mean/stddev, then scores every transaction against 6 detection signals: amount anomaly (3-sigma), new counterparty (concentrated payment patterns), service type deviation, velocity spike (rolling window), burst pattern (runaway loop detection), and time anomaly (off-hours activity). Graduated response: info → warning → critical (circuit breaker). Per-agent sharded locks allow concurrent detection across agents. Alerts integrate with existing SIEM via webhook.

### Event Bus
Durable, ordered event bus decoupling payment settlement from downstream processing. Replaces fire-and-forget goroutines with batched, backpressure-aware consumption. Settlement events are published once and fan out to multiple consumers (forensics, chargeback). In-memory channel-based implementation with 10K event buffer; Kafka-compatible interface for production multi-node deployments. Prometheus metrics: publish rate, consumer lag, batch sizes, dropped events.

### Materialized Views
Pre-aggregated data for dashboard analytics and reporting. `billing_timeseries_hourly` replaces full table scans on gateway_request_logs. `chargeback_summary_monthly` replaces N+1 queries in report generation. Refreshed periodically by the reconciliation timer.
