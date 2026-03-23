import {
  Activity,
  AlertTriangle,
  CheckCircle,
  XCircle,
  Shield,
  Database,
  RefreshCw,
} from "lucide-react";
import { useSystemHealth } from "@/hooks/api/use-dashboard";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { relativeTime } from "@/lib/utils";

function checkValueColor(value: number): string {
  if (value === 0) return "text-[var(--foreground)]";
  if (value <= 2) return "text-[var(--color-warning)]";
  return "text-[var(--color-danger)]";
}

export function HealthPage() {
  const health = useSystemHealth();
  const h = health.data;

  const overallVariant =
    h?.status === "healthy"
      ? "success"
      : h?.status === "degraded"
        ? "warning"
        : "danger";

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--border)] px-8 py-5">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-[16px] font-semibold text-[var(--foreground)]">
              System Health
            </h1>
            <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
              Infrastructure status, reconciliation, and conservation invariants
            </p>
          </div>
          <button
            onClick={() => health.refetch()}
            disabled={health.isFetching}
            className="flex items-center gap-1.5 rounded-[var(--radius-md)] border border-[var(--border)] bg-[var(--background)] px-3 py-1.5 text-[12px] text-[var(--foreground-muted)] hover:text-[var(--foreground)] hover:border-[var(--foreground-muted)] transition-colors disabled:opacity-50"
          >
            <RefreshCw
              size={12}
              className={health.isFetching ? "animate-spin" : ""}
            />
            Refresh
          </button>
        </div>
      </header>

      <div className="px-8 py-6 space-y-6">
        {/* Overall status banner */}
        {health.isLoading ? (
          <Skeleton className="h-20 rounded-[var(--radius-lg)]" />
        ) : health.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] py-8 text-[13px] text-[var(--color-danger)]">
            <AlertTriangle size={14} />
            Failed to load system health
          </div>
        ) : (
          <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <Activity
                  size={18}
                  strokeWidth={1.5}
                  className="text-[var(--foreground-muted)]"
                />
                <div>
                  <p className="text-[14px] font-medium text-[var(--foreground)]">
                    Overall Status
                  </p>
                  <p className="text-[12px] text-[var(--foreground-muted)]">
                    {h?.services.length ?? 0} subsystems monitored
                  </p>
                </div>
              </div>
              <Badge variant={overallVariant}>
                {h?.status === "healthy"
                  ? "All Systems Operational"
                  : h?.status === "degraded"
                    ? "Degraded"
                    : "Unhealthy"}
              </Badge>
            </div>
          </div>
        )}

        {/* Subsystem status cards */}
        <div>
          <h2 className="text-[14px] font-medium text-[var(--foreground)] mb-3">
            Subsystems
          </h2>
          {health.isLoading ? (
            <div className="grid grid-cols-3 gap-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-[var(--radius-lg)]" />
              ))}
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {(h?.services ?? []).map((svc) => (
                <div
                  key={svc.name}
                  className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-4"
                >
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Database
                        size={14}
                        className="text-[var(--foreground-muted)]"
                      />
                      <span className="text-[13px] font-medium text-[var(--foreground)]">
                        {svc.name}
                      </span>
                    </div>
                    {svc.status === "up" ? (
                      <CheckCircle
                        size={14}
                        className="text-[var(--color-success)]"
                      />
                    ) : (
                      <XCircle
                        size={14}
                        className="text-[var(--color-danger)]"
                      />
                    )}
                  </div>
                  <Badge
                    variant={svc.status === "up" ? "success" : "danger"}
                  >
                    {svc.status}
                  </Badge>
                  {svc.detail && (
                    <p className="mt-2 text-[11px] text-[var(--foreground-muted)] truncate">
                      {svc.detail}
                    </p>
                  )}
                </div>
              ))}
              {(h?.services ?? []).length === 0 && (
                <p className="col-span-3 text-center text-[12px] text-[var(--foreground-muted)] py-8">
                  No subsystem data available
                </p>
              )}
            </div>
          )}
        </div>

        {/* Reconciliation status */}
        <div>
          <h2 className="text-[14px] font-medium text-[var(--foreground)] mb-3">
            <div className="flex items-center gap-2">
              <Shield size={14} />
              Reconciliation
            </div>
          </h2>
          {health.isLoading ? (
            <Skeleton className="h-40 rounded-[var(--radius-lg)]" />
          ) : h?.reconciliation ? (
            <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
              <div className="flex items-center justify-between mb-4">
                <Badge
                  variant={h.reconciliation.healthy ? "success" : "danger"}
                >
                  {h.reconciliation.healthy
                    ? "All Checks Passing"
                    : "Issues Detected"}
                </Badge>
                <span className="text-[11px] text-[var(--foreground-muted)] tabular-nums">
                  Last run: {relativeTime(h.reconciliation.timestamp)}
                </span>
              </div>

              <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
                {[
                  {
                    label: "Ledger Mismatches",
                    value: h.reconciliation.ledgerMismatches,
                  },
                  {
                    label: "Stuck Escrows",
                    value: h.reconciliation.stuckEscrows,
                  },
                  {
                    label: "Stale Streams",
                    value: h.reconciliation.staleStreams,
                  },
                  {
                    label: "Orphaned Holds",
                    value: h.reconciliation.orphanedHolds,
                  },
                  {
                    label: "Invariant Violations",
                    value: h.reconciliation.invariantViolations,
                  },
                ].map((check) => (
                  <div
                    key={check.label}
                    className="rounded-[var(--radius-md)] border border-[var(--border-subtle)] bg-[var(--background)] p-3 text-center"
                  >
                    <p
                      className={`text-[20px] font-semibold tabular-nums ${checkValueColor(check.value)}`}
                    >
                      {check.value}
                    </p>
                    <p className="text-[11px] text-[var(--foreground-muted)] mt-1">
                      {check.label}
                    </p>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5 text-center">
              <p className="text-[12px] text-[var(--foreground-muted)]">
                No reconciliation data yet. First run happens within 5 minutes
                of server start.
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
