# Session Keys

Bounded autonomy for AI agents. Delegated spending with cryptographic proof and hierarchical permissions.

## Overview

Session keys let an agent owner create a temporary, constrained keypair that can spend funds on their behalf. The owner sets spending limits (per-transaction, per-day, total), recipient restrictions, and an expiration time. The session key holder proves authorization by signing each transaction with ECDSA — no API key needed for the actual spend.

Session keys can delegate to child keys with narrower permissions, forming a tree. Revoking a parent instantly cascades to all descendants.

## Concepts

### Key Lifecycle

```
Owner creates key (publicKey + permissions)
    │
    ▼
  Active ──── Transact (signed) ────▶ Usage recorded
    │                                     │
    │                                     ▼
    │                              Limits enforced
    │                          (per-tx, daily, total)
    │
    ├── Revoke ──▶ Revoked (cascades to children)
    │
    └── Time passes ──▶ Expired (lazy check on next use)
```

No background cleanup — expiration is checked at validation time.

### Permission Model

| Field | Example | Description |
|-------|---------|-------------|
| `MaxPerTransaction` | `"1.00"` | Max USDC per single transaction |
| `MaxPerDay` | `"10.00"` | Max USDC per calendar day (lazy reset) |
| `MaxTotal` | `"100.00"` | Lifetime spend cap |
| `ExpiresAt` | RFC3339 | Hard expiration (required) |
| `ValidAfter` | RFC3339 | Not usable before this time |
| `AllowedRecipients` | `["0x..."]` | Specific addresses only |
| `AllowedServiceTypes` | `["translation"]` | Service categories only |
| `AllowAny` | `true` | No recipient restrictions |

At least one of `AllowedRecipients`, `AllowedServiceTypes`, or `AllowAny` must be set.

### Signature Protocol

Every transaction is signed with the session key's private key:

```
message = "Alancoin|{to}|{amount}|{nonce}|{timestamp}"
signature = ECDSA_Sign(Keccak256("\x19Ethereum Signed Message:\n" + len(message) + message), privateKey)
```

The server recovers the signer address and checks it matches the registered public key. Nonce must be strictly increasing (replay protection). Timestamp must be within 5 minutes (freshness).

### Delegation (Agent-to-Agent)

Session keys can create child keys with **narrower** permissions:

```
Root key (owner)
├── Child A (translator, $50 budget)
│   └── Grandchild A1 (translator, $10 budget)
└── Child B (inference, $30 budget)
```

Rules:
- Child budget ≤ parent's remaining budget
- Child service types ⊆ parent's service types
- Child recipients ⊆ parent's recipients
- Child cannot outlive parent
- Max depth: 5 levels
- Delegation is signed by the parent key's private key

When a child spends, usage cascades up to all ancestors. This ensures the parent's budget is consumed even when children spend.

## Transact Flow (Two-Phase Hold)

```
1. Lock key chain (leaf + all ancestors)
2. ValidateSigned()
   ├── Verify ECDSA signature
   ├── Check nonce > lastNonce
   ├── Check timestamp within 5 min
   ├── Check per-tx / daily / total limits
   ├── Check recipient restrictions
   └── For delegated: validate ancestor chain
3. Ledger Hold (available → pending)
4. On-chain transfer (or synthetic in demo mode)
5a. Success: ConfirmHold (pending → total_out)
             Deposit to recipient
             Record transaction
             Accumulate revenue (for staking)
5b. Failure: ReleaseHold (pending → available)
6. RecordUsageWithCascade (update leaf + all ancestors)
7. Unlock
```

Usage is recorded **after** the transfer succeeds. If the transfer fails, no budget is consumed.

### Daily Limit Reset

Daily spend (`SpentToday`) resets lazily. On each validation, the system compares `LastResetDay` to today's date. If different, `SpentToday` is treated as zero. The field is updated in `RecordUsage()` after a successful transaction.

## API Reference

### Create Session Key

```
POST /v1/agents/:address/sessions
Authorization: Bearer <api_key>
```

```json
{
  "publicKey": "0xSessionKeyAddress...",
  "maxPerTransaction": "1.00",
  "maxPerDay": "10.00",
  "maxTotal": "100.00",
  "expiresIn": "7d",
  "allowedServiceTypes": ["translation", "inference"],
  "label": "Translation budget Q1"
}
```

`expiresIn` accepts duration strings (`"7d"`, `"24h"`). Alternatively use `expiresAt` with an RFC3339 timestamp. Defaults to 24 hours.

### List Session Keys

```
GET /v1/agents/:address/sessions
```

### Get Session Key

```
GET /v1/agents/:address/sessions/:keyId
```

### Revoke Session Key

```
DELETE /v1/agents/:address/sessions/:keyId
Authorization: Bearer <api_key>
```

Cascades to all child keys in the delegation tree.

### Transact (Signed)

```
POST /v1/agents/:address/sessions/:keyId/transact
```

```json
{
  "to": "0xRecipient...",
  "amount": "0.50",
  "serviceId": "svc_xyz",
  "nonce": 1,
  "timestamp": 1707234567,
  "signature": "0x..."
}
```

No API key required — the ECDSA signature proves authorization.

**Response (200):**
```json
{
  "status": "executed",
  "txHash": "0x...",
  "verification": {
    "signatureValid": true,
    "nonceValid": true,
    "timestampValid": true
  },
  "permissions": {
    "remainingDaily": "9.50",
    "remainingTotal": "99.50"
  },
  "usage": {
    "transactionCount": 1,
    "totalSpent": "0.50",
    "spentToday": "0.50"
  }
}
```

### Create Delegation

```
POST /v1/sessions/:keyId/delegate
```

```json
{
  "publicKey": "0xChildKeyAddress...",
  "maxTotal": "50.00",
  "maxPerTransaction": "0.50",
  "maxPerDay": "5.00",
  "nonce": 1,
  "timestamp": 1707234567,
  "signature": "0x...",
  "delegationLabel": "Translator bot"
}
```

Signed by the parent key's private key. Message format: `"AlancoinDelegate|{childPubKey}|{maxTotal}|{nonce}|{timestamp}"`.

### Get Delegation Tree

```
GET /v1/sessions/:keyId/tree
```

Returns the full tree structure rooted at this key.

## Architecture

### Locking Strategy

Two lock modes prevent concurrent spending from exceeding budgets:

| Mode | Used When | What's Locked |
|------|-----------|---------------|
| `LockKey(id)` | Root key transaction | Single key mutex |
| `LockKeyChain(id)` | Delegated key transaction | Leaf + all ancestor mutexes |

`LockKeyChain` prevents two sibling keys from concurrently exceeding their parent's budget. Both siblings would need the parent lock, so one blocks until the other completes.

### Cascade Usage Recording

When a delegated key spends, `RecordUsageWithCascade` walks up the tree:

```
Grandchild spends $1
  → Grandchild.TotalSpent += $1
  → Child.TotalSpent += $1
  → Root.TotalSpent += $1
```

Each ancestor's budget is checked before incrementing. If any ancestor would exceed its `MaxTotal`, the entire cascade is rejected.

### Error Types

All errors are `ValidationError` structs with a `Code` field for programmatic handling:

| Code | Meaning |
|------|---------|
| `key_not_found` | Key doesn't exist |
| `key_revoked` / `key_expired` | Key is inactive |
| `exceeds_per_tx` | Amount > MaxPerTransaction |
| `exceeds_daily` | Would exceed MaxPerDay |
| `exceeds_total` | Would exceed MaxTotal |
| `recipient_not_allowed` | Recipient not in allowed list |
| `invalid_signature` | Signature verification failed |
| `signature_mismatch` | Recovered signer ≠ registered key |
| `nonce_reused` | Nonce ≤ last used nonce |
| `max_depth_exceeded` | Delegation tree too deep (>5) |
| `child_exceeds_parent` | Child budget > parent's remaining |
| `ancestor_invalid` | An ancestor is revoked or expired |

### Integration Points

| Package | Interface | Purpose |
|---------|-----------|---------|
| Ledger | `BalanceService` | Hold/ConfirmHold/ReleaseHold/Deposit |
| Wallet | `WalletService` | On-chain USDC transfer |
| Registry | `TransactionRecorder` | Record tx for reputation |
| Stakes | `RevenueAccumulator` | Intercept seller revenue |
| Realtime | `EventEmitter` | Broadcast transaction events |

### Database Schema

```sql
CREATE TABLE session_keys (
    id                  VARCHAR(36) PRIMARY KEY,
    owner_address       VARCHAR(42) NOT NULL,
    public_key          TEXT NOT NULL,
    max_per_transaction VARCHAR(32),
    max_per_day         VARCHAR(32),
    max_total           VARCHAR(32),
    valid_after         TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ NOT NULL,
    allowed_recipients  TEXT[],
    allowed_service_types TEXT[],
    allow_any           BOOLEAN DEFAULT FALSE,
    label               VARCHAR(255),
    transaction_count   INTEGER DEFAULT 0,
    total_spent         VARCHAR(32) DEFAULT '0',
    spent_today         VARCHAR(32) DEFAULT '0',
    last_reset_day      VARCHAR(10),
    last_used           TIMESTAMPTZ,
    last_nonce          BIGINT DEFAULT 0,
    revoked_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    parent_key_id       VARCHAR(36) REFERENCES session_keys(id),
    depth               INTEGER DEFAULT 0,
    root_key_id         VARCHAR(36),
    delegation_label    VARCHAR(255)
);

CREATE TABLE delegation_log (
    id              SERIAL PRIMARY KEY,
    parent_key_id   VARCHAR(36) NOT NULL,
    child_key_id    VARCHAR(36) NOT NULL,
    root_key_id     VARCHAR(36) NOT NULL,
    root_owner_addr VARCHAR(42) NOT NULL,
    depth           INTEGER NOT NULL,
    max_total       VARCHAR(40),
    reason          VARCHAR(255),
    created_at      TIMESTAMPTZ DEFAULT NOW()
);
```

## Constraints & Safety

- **ECDSA signature required** — No transaction without cryptographic proof of key possession
- **Strictly increasing nonce** — Prevents replay attacks
- **5-minute timestamp window** — Prevents stale signature reuse
- **Per-key and chain-level mutex** — Prevents concurrent overspend
- **Budget can only narrow** — Children cannot exceed parent permissions
- **Cascade revocation** — Revoking parent immediately disables all descendants
- **Hold-before-transfer** — Funds reserved before on-chain execution, released on failure
- **Usage recorded after success** — Failed transfers don't consume budget
- **Ancestor validation** — Every delegated transaction checks the full ancestor chain is still active

## Testing

- `crypto_test.go` — Signature creation, recovery, replay protection, permission validation
- `manager_test.go` — Create, revoke, usage tracking, spending limits, nonce replay
- `delegation_test.go` — A2A delegation, budget narrowing, tree operations, concurrent siblings

## File Layout

```
internal/sessionkeys/
├── types.go          # Types: SessionKey, Permission, Usage, ValidationError
├── crypto.go         # ECDSA signing and verification (EIP-191)
├── manager.go        # Business logic: create, validate, revoke, delegate, cascade
├── handlers.go       # HTTP endpoints and two-phase hold transaction flow
├── store.go          # MemoryStore (demo/testing)
├── postgres_store.go # PostgreSQL store
├── crypto_test.go    # Signature tests
├── manager_test.go   # Manager unit tests
├── delegation_test.go# Delegation tree tests
└── SESSIONKEYS.md    # This file
```

---

## Not Yet Built

### P0 — Policy Engine (Replace Hardcoded Constraints)

Session key permissions are a fixed set of fields (`MaxPerTransaction`, `MaxPerDay`, `AllowedRecipients`, etc.). This works for simple cases but enterprises need **arbitrary, composable spending rules**. The current model should evolve into a policy engine:

- **Rule-based policies** — Instead of flat fields, define policies as a list of rules: `[{condition: "amount > 100", action: "deny"}, {condition: "time.hour < 6 || time.hour > 22", action: "require_approval"}, {condition: "recipient.reputation < 30", action: "deny"}]`.
- **Policy templates** — Pre-built policy sets: "Conservative" (low limits, known recipients only), "Aggressive" (high limits, any recipient), "Compliance" (travel rule enforcement above $3k).
- **Dynamic policy evaluation** — Policies can reference external data: recipient reputation score, current gas prices, historical spending patterns.
- **Policy versioning** — When a policy is updated, existing session keys can either inherit the new policy or be pinned to the old one.

This is the enterprise killer feature. Companies don't buy "you can set a $10 daily limit." They buy "you can write any rule you want about how your agents spend money."

**Interface sketch:**
```go
type PolicyEngine interface {
    Evaluate(ctx context.Context, tx TransactionRequest, key *SessionKey) (*PolicyDecision, error)
}

type PolicyDecision struct {
    Action     string // "allow", "deny", "require_approval", "alert"
    Reason     string
    MatchedRule string
}
```

### P0 — Anomaly Detection / Real-Time Risk Scoring

Session keys have no intelligence about whether a transaction is suspicious. An agent with a $10k daily limit could drain the entire budget in one burst of 100 transactions and nothing would flag it until the budget is gone.

**What needs to exist:**

- **Velocity checks** — If an agent's spending rate in the last 5 minutes is >10x its 24-hour average, flag or block.
- **Recipient anomaly** — First-time recipient + large amount = elevated risk score.
- **Time-of-day patterns** — Agent normally transacts 9am-6pm, sudden 3am burst = flag.
- **Budget burn rate** — If the agent will exhaust its daily/total budget within the next hour at current pace, emit a warning event.
- **Risk score per transaction** — 0.0-1.0 score computed before execution. Above threshold → block or require human approval.

This feeds into a broader `internal/risk/` package that doesn't exist yet. Session keys would call `riskEngine.Score(tx)` during `ValidateSigned()`.

### P0 — Key Rotation Without Identity Loss

Currently there's no way to rotate a session key. If a key is compromised, you revoke it and create a new one — but the new key has zero usage history, fresh nonces, and no connection to the old key. For enterprise:

- **Key rotation** — `RotateKey(oldKeyID) → newKey` that transfers remaining budget, inherits delegation tree, and maintains the usage lineage.
- **Grace period** — Both old and new key are valid during a rotation window (e.g., 5 minutes) so in-flight transactions don't fail.
- **Rotation audit** — Rotation events logged with reason, old/new key IDs, and who initiated it.

### P0 — Budget Alerts and Notifications

No alerting when keys approach limits:

- **Threshold webhooks** — When a key hits 50%, 75%, 90% of its `MaxTotal`, emit a webhook to the owner.
- **Daily spend alerts** — When `SpentToday` exceeds a configurable percentage of `MaxPerDay`.
- **Expiration warnings** — 24h, 1h before expiry, notify the owner so they can renew.
- **Delegation budget alerts** — When a child key's spending approaches the parent's remaining budget.

### P1 — Spend Analytics Dashboard

No visibility into how session keys are being used:

- **Per-key analytics** — Spending over time, top recipients, average transaction size, usage patterns.
- **Per-owner analytics** — Across all keys: total delegated, total spent, top spenders in the delegation tree.
- **Delegation tree visualization** — Which branches of the tree are consuming the most budget?
- **Endpoints:** `GET /v1/agents/:address/sessions/analytics`, `GET /v1/sessions/:keyId/analytics`

### P1 — Delegation Audit Log

Delegation events (create child, revoke child, cascade revocation) are not recorded in a queryable audit log. The `delegation_log` table exists but it only records creation, not revocations or budget cascade events.

- Record every delegation event: create, revoke, cascade_revoke, budget_exceeded
- Include the full ancestor chain at time of event
- Queryable by root owner, time range, event type
- Required for compliance: "show me every delegation action taken by keys I own in the last 30 days"

### P1 — Session Key Scopes (Read vs Write)

Current keys are all-or-nothing for spending. But agents may need keys with different permission levels:

- **Read-only keys** — Can query balances, list transactions, discover services. Cannot spend.
- **Approval keys** — Can approve pending transactions but not initiate them.
- **Scoped keys** — Can only call specific API endpoints (e.g., only `/v1/streams`, not `/v1/escrow`).

### P2 — Hardware Security Module (HSM) Integration

Session key private keys are held in software. For enterprise deployments handling large budgets:

- HSM-backed key generation and signing (AWS CloudHSM, Azure Key Vault, YubiKey)
- Key material never leaves the HSM
- Signature requests go through the HSM API
- This is table stakes for any financial platform handling >$1M in agent transactions

### P2 — Cross-Platform Session Keys

Session keys only work within the Alancoin platform. For agents operating across multiple platforms:

- **Portable credentials** — A session key signed by Alancoin that can be verified by external platforms.
- **DID integration** — Session keys as Verifiable Credentials linked to the agent's DID.
- This connects to the broader agent identity system that doesn't exist yet.
