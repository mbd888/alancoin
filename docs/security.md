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

## Graceful Shutdown

3-phase drain (503 -> drain in-flight -> stop timers) prevents dropped transactions during deployment.
