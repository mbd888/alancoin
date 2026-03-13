# Session Keys

Session keys are the core safety primitive for bounded agent autonomy. Instead of giving an agent full wallet access, you create a session key with hard constraints.

## Creating a Session Key

```python
from alancoin.admin import Alancoin

client = Alancoin("http://localhost:8080", api_key="ak_...")

key = client.create_session_key(
    agent_address="0xMyAgent",
    expires_in="24h",
    max_per_transaction="1.00",
    max_per_day="10.00",
    max_total="50.00",
    allowed_service_types=["translation", "summarization"],
    allowed_recipients=["0xTrustedSeller"],
)
```

## Constraints

Every session key enforces:

- **Budget limits**: `max_per_transaction`, `max_per_day`, `max_total`
- **Time expiry**: key becomes invalid after the deadline
- **Service restrictions**: `allowed_service_types` whitelist
- **Recipient restrictions**: `allowed_recipients` whitelist
- **Scopes**: fine-grained permission scopes

## Hierarchical Delegation

Session keys support delegation up to 5 levels deep. An agent with a session key can create child keys for sub-agents, each with narrower permissions than the parent.

```
Root Key (max_total: $50, services: [translation, inference, summarization])
  |
  +-- Child A (max_total: $20, services: [translation, inference])
  |     |
  |     +-- Grandchild A1 (max_total: $5, services: [translation])
  |
  +-- Child B (max_total: $10, services: [summarization])
```

**Monotonic attenuation**: child keys can only have equal or narrower permissions than their parent. A child cannot increase budgets, extend expiry, or add service types that the parent doesn't have.

Revoking a parent cascades to all descendants.

## HMAC-Chain Delegation Proofs

Delegation is verified using macaroon-inspired HMAC chains:

```
tag_0 = HMAC(rootSecret, canonicalize(caveat_0))
tag_1 = HMAC(tag_0, canonicalize(caveat_1))
tag_n = HMAC(tag_{n-1}, canonicalize(caveat_n))
```

Verification recomputes the chain from the root secret and compares the final tag. This gives O(1) ancestor verification -- you only need the root secret and the proof, not the full delegation tree.

Caveats are canonicalized with deterministic JSON (sorted keys, sorted slices) for reproducible HMAC computation.

## Key Rotation

Keys can be rotated (swap the underlying keypair) without disrupting the session. The old key has a grace period before it becomes invalid, allowing in-flight transactions to complete.

## Transactions

Session key transactions use ECDSA signatures with:
- **Monotonic nonces**: prevent replay attacks
- **5-minute timestamp freshness**: prevent stale transaction replay
- **Instant revocation**: key can be revoked at any time, cascading to children

## Spend Policies

Attach fine-grained spend policies to session keys or tenants:

| Policy Type | Description |
|-------------|-------------|
| `time_window` | Restrict requests to specific hours/days |
| `rate_limit` | Cap requests per time window |
| `service_allowlist` | Whitelist service types |
| `service_blocklist` | Blacklist service types |
| `max_requests` | Absolute request count cap |
| `spend_velocity` | Maximum USDC per time window |

Policies support **shadow mode**: evaluate and log decisions without enforcing them, useful for testing new rules in production.
