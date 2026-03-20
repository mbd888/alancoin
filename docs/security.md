# Security

## Two-Phase Holds

Funds move `available -> pending` before any transfer. Confirmed on success, released on failure. No double-spend. No stuck funds.

## Session Keys

ECDSA signatures with monotonic nonces (replay-proof), 5-minute timestamp freshness window, instant revocation with cascading to child keys, and key rotation without session disruption.

Session keys support **hierarchical delegation** up to 5 levels deep. Each level uses macaroon-inspired HMAC-chain delegation proofs with monotonic attenuation -- child keys can only have narrower permissions than their parent. See [Session Keys](session-keys.md) for details.

## Ledger Integrity

NUMERIC(20,6) columns with serializable isolation level. `CHECK available >= 0` constraint at the database level prevents overdraft regardless of application logic.

## Escrow Protection

Every service call through the gateway is escrow-backed. Funds are locked until the buyer confirms delivery or the timeout expires. Supports arbitration with evidence submission and partial settlement.

## Supervisor

5 behavioral rules evaluate every transaction before execution:

| Rule | What it does |
|------|-------------|
| **Velocity** | Tier-based hourly spend limits ($50/hr for new agents up to $100K/hr for elite) |
| **New Agent** | Caps new-tier agents at $5/transaction |
| **Baseline** | ML anomaly detection -- denies if projected hourly spend exceeds mean + 3 sigma (requires 24h history) |
| **Circular Flow** | Flags A->B->C->A payment cycles within 1 hour |
| **Counterparty Concentration** | Flags when >80% of volume goes to a single counterparty |

Denials are logged with feature vectors and an `OverrideAllowed` label for ML training data export.

## Input Validation

- Ethereum address format validation
- Parameterized SQL everywhere (no string concatenation)
- 1MB request size limit

## Rate Limiting

Per-tenant rate limiting with plan-based defaults. Token bucket with burst support.

| Plan | Rate Limit |
|------|-----------|
| Free | 60 RPM |
| Starter | 300 RPM |
| Growth | 1000 RPM |
| Enterprise | Custom |

## Security Headers

CSP, X-Frame-Options, X-Content-Type-Options, XSS protection, CORS.

## DNS Rebinding Protection

Webhook delivery URLs are re-validated at each attempt to prevent SSRF.

## Cryptographic Receipts

HMAC-SHA256 signed receipts on every payment path (gateway, escrow, stream, session key) for auditability.

## Circuit Breaker

Per-key circuit breaker (closed -> open -> half-open) prevents cascade failures on downstream services.

## KYA (Know Your Agent) Identity

HMAC-signed identity certificates bind AI agents to legal entities with verifiable permission scopes and TraceRank reputation snapshots. Certificates auto-expire and are revoked when the underlying session key is invalidated. Trust tiers (AAA-D) gate escrow requirements: AAA agents qualify for instant settlement, D-tier agents require full escrow.

Compliance exports produce EU AI Act Article 12 technical documentation packages with: agent DID, organizational authorization chain, permission boundaries, behavioral reputation, and signature verification status.

## FinOps Chargeback

Per-department cost attribution with real-time budget enforcement at the ledger level. Cost center monthly budgets are enforced as hard limits — spend events are rejected when the allocation is exhausted. Tenant isolation ensures each tenant can only view and manage their own cost centers. Warning thresholds trigger alerts before budget exhaustion. All budget enforcement decisions are logged for audit trail.

## Dispute Arbitration

Two-track resolution: auto-resolve by comparing service output against behavioral contract invariants (no human needed), or assign a human/agent arbiter with evidence-based decision-making. Financial outcomes execute atomically via escrow primitives. Losers receive TraceRank reputation penalties proportional to the disputed amount.

## Spend Forensics

Real-time anomaly detection on agent payment patterns. Six detection signals: amount anomaly (3-sigma), new counterparty from concentrated agent, service type deviation, velocity spike, burst pattern (runaway loop), and time anomaly. Uses Welford's online algorithm for statistically stable baselines without storing full history. Graduated severity: info (logged) → warning (alert) → critical (circuit breaker + escrow freeze). All alerts include sigma deviation, baseline vs actual values, and forensic context for incident response.

Forensics is automatically integrated into the gateway service via the `ForensicsIngestor` interface — every successful proxy payment is analyzed for anomalies without any manual integration. Alerts are available via REST API and can be forwarded to external SIEM systems via webhook.

## Production Security Hardening

The following protections are enforced in production (`ENV=production`):
- `DEMO_MODE=true` rejected at startup
- `ALLOW_LOCAL_ENDPOINTS=true` rejected at startup (prevents SSRF)
- CORS defaults to deny-all (must explicitly configure `CORS_ALLOWED_ORIGINS`)
- Database SSL warning if `DATABASE_URL` lacks `sslmode=require`
- Internal error messages never exposed to API callers (generic "Internal server error" returned)
- Failed auth attempts logged with client IP and path
- Per-IP WebSocket connection limit (100 per IP)
- Rate limiting on all session key delegation endpoints
- Tenant isolation enforced on all plugin endpoints (KYA, chargeback, arbitration)

## Graceful Shutdown

3-phase drain (503 -> drain in-flight -> stop timers) prevents dropped transactions during deployment.
