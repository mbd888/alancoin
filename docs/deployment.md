# Deployment

## Quick Start (In-Memory)

```bash
git clone https://github.com/mbd888/alancoin.git && cd alancoin
make deps && make run
# Server at http://localhost:8080 -- no database, no config
```

Without `DATABASE_URL`, the server runs fully in-memory. No external dependencies for development or demos.

## Docker Compose (Local Dev)

```bash
docker-compose up
# Starts: PostgreSQL, Alancoin server, Prometheus, Grafana
# Server at :8080, Prometheus at :9090, Grafana at :3000
```

## Docker

```bash
docker build -t alancoin .
docker run -p 8080:8080 -e PRIVATE_KEY=your_hex_key alancoin
```

## Fly.io

```bash
fly launch --no-deploy --copy-config --name alancoin
fly secrets set PRIVATE_KEY=your_hex_key
fly postgres create --name alancoin-db && fly postgres attach alancoin-db
fly deploy
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `ENV` | Environment (`development`, `staging`, `production`) | `development` |
| `LOG_LEVEL` | Log level | `info` |
| `DATABASE_URL` | PostgreSQL connection string | In-memory mode |
| `PRIVATE_KEY` | Wallet private key (hex) | Required |
| `RPC_URL` | Ethereum RPC endpoint | Base Sepolia |
| `CHAIN_ID` | Chain ID | `84532` (Base Sepolia) |
| `USDC_CONTRACT` | USDC token address | Base Sepolia USDC |
| `ADMIN_SECRET` | Admin API secret | None |
| `RECEIPT_HMAC_SECRET` | Signs payment receipts | None |
| `REPUTATION_HMAC_SECRET` | Signs reputation responses | None |
| `PLATFORM_ADDRESS` | Fee collection address | `0x...001` |
| `RATE_LIMIT_RPM` | Global rate limit | `100` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry collector | Disabled |
| `POSTGRES_MAX_OPEN_CONNS` | DB pool size | `25` |
| `REQUEST_TIMEOUT` | Handler execution timeout | `30s` |

## Database

```bash
make db-setup      # Set up local PostgreSQL
make db-migrate    # Run migrations (requires DATABASE_URL)
make db-rollback   # Rollback last migration
```

PostgreSQL uses serializable isolation and `CHECK available >= 0` constraints to prevent overdraft at the database level.

### Migrations (001-050, 40 files)

Core: agent_balances, ledger_entries, sessions, escrow, receipts, webhooks, policies, tenants.
Plugins: kya_certificates (047), chargeback (048), arbitration_cases (049), forensics (050).

## Dashboard Frontend

```bash
cd dashboard
nvm use 20
npm install
npm run dev     # Dev server at :5173, proxies /v1/* to :8080
npm run build   # Static build to dist/
npm test        # 54 Vitest tests
```

Stack: Vite 7 + React 19 + TanStack Router/Query + Tailwind CSS v4 + Recharts. Builds to static files served by Go or CDN.

## CI Pipeline

The CI pipeline runs 5 jobs on every push/PR to main:

1. **lint** -- go vet, golangci-lint, govulncheck
2. **test** -- unit + integration tests with PostgreSQL service container (40% coverage gate)
3. **sdk-test** -- Python SDK pytest
4. **harness-test** -- experiment harness pytest
5. **build** -- binary + Docker image build (requires all prior jobs)
