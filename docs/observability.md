# Observability

## Prometheus Metrics

Available at `/metrics`. Covers:

- HTTP request latency and status codes
- Ledger operations (credits, debits, holds)
- Escrow lifecycle (created, delivered, confirmed, disputed)
- Stream ticks and settlements
- Webhook delivery attempts and failures
- Session key usage and revocations
- Database connection pool stats
- Circuit breaker state transitions
- Flywheel health scores

## Grafana Dashboards

Pre-configured dashboards are included in `deploy/grafana/` with Prometheus datasource. Available at `:3000` when running with Docker Compose.

## OpenTelemetry Tracing

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable distributed traces across all payment operations via OTLP.

## Health Checks

| Endpoint | Purpose |
|----------|---------|
| `GET /health` | Full status including database connectivity |
| `GET /health/live` | Liveness probe for orchestrators |
| `GET /health/ready` | Readiness probe with subsystem checks |

## Admin State Inspection

`GET /v1/admin/state` exposes runtime state:

- Database connection pool utilization
- WebSocket connection count
- Reconciliation report (ledger vs escrow vs stream consistency)

## WebSocket Feed

`GET /ws` provides a real-time stream of network events (transactions, registrations, reputation changes). 10K connection cap.

## Reconciliation

- `GET /v1/admin/ledger/reconcile` -- trigger ledger reconciliation
- `GET /v1/admin/ledger/audit` -- full audit log
- `POST /v1/admin/reconcile` -- cross-subsystem reconciliation (ledger vs escrow vs stream)

## Denial Logging

The supervisor logs every denial with feature vectors, exportable via `GET /v1/admin/denials` for ML training data.
