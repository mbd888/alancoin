import { useState, useEffect } from "react";
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
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/layouts/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { relativeTime } from "@/lib/utils";

function checkValueColor(value: number): string {
  if (value === 0) return "text-foreground";
  if (value <= 2) return "text-warning";
  return "text-destructive";
}

function FreshnessLabel({ dataUpdatedAt }: { dataUpdatedAt?: number }) {
  const [, setTick] = useState(0);

  useEffect(() => {
    if (!dataUpdatedAt) return;
    const id = window.setInterval(() => setTick((t) => t + 1), 10_000);
    return () => window.clearInterval(id);
  }, [dataUpdatedAt]);

  if (!dataUpdatedAt) return null;

  const diff = Math.floor((Date.now() - dataUpdatedAt) / 1000);
  const label = diff < 10 ? "just now" : diff < 60 ? `${diff}s ago` : `${Math.floor(diff / 60)}m ago`;

  return (
    <span className="text-xs text-muted-foreground/50">
      Updated {label}
    </span>
  );
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
      <PageHeader
        icon={Activity}
        title="System Health"
        description="Infrastructure status, reconciliation, and conservation invariants"
        actions={
          <div className="flex items-center gap-3">
            <FreshnessLabel dataUpdatedAt={health.dataUpdatedAt} />
            <Button
              variant="secondary"
              size="sm"
              onClick={() => health.refetch()}
              disabled={health.isFetching}
            >
              <RefreshCw
                size={12}
                className={health.isFetching ? "animate-spin" : ""}
              />
              Refresh
            </Button>
          </div>
        }
      />

      <div className="px-4 md:px-8 py-6 space-y-6">
        {/* Overall status banner */}
        {health.isLoading ? (
          <Skeleton className="h-20 rounded-lg" />
        ) : health.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load system health
            <Button variant="ghost" size="sm" onClick={() => health.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <div className="rounded-lg border bg-card p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <Activity
                  size={18}
                  strokeWidth={1.5}
                  className="text-muted-foreground"
                />
                <div>
                  <p className="text-sm font-medium text-foreground">
                    Overall Status
                  </p>
                  <p className="text-xs text-muted-foreground">
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
          <h2 className="text-sm font-medium text-foreground mb-3">
            Subsystems
          </h2>
          {health.isLoading ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-lg" />
              ))}
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {(h?.services ?? []).map((svc) => (
                <div
                  key={svc.name}
                  className="rounded-lg border bg-card p-4"
                >
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Database
                        size={14}
                        className="text-muted-foreground"
                      />
                      <span className="text-sm font-medium text-foreground">
                        {svc.name}
                      </span>
                    </div>
                    {svc.status === "up" ? (
                      <CheckCircle
                        size={14}
                        className="text-success"
                      />
                    ) : (
                      <XCircle
                        size={14}
                        className="text-destructive"
                      />
                    )}
                  </div>
                  <Badge
                    variant={svc.status === "up" ? "success" : "danger"}
                  >
                    {svc.status}
                  </Badge>
                  {svc.detail && (
                    <p className="mt-2 text-xs text-muted-foreground truncate">
                      {svc.detail}
                    </p>
                  )}
                </div>
              ))}
              {(h?.services ?? []).length === 0 && (
                <p className="col-span-3 text-center text-xs text-muted-foreground py-8">
                  No subsystem data available
                </p>
              )}
            </div>
          )}
        </div>

        {/* Reconciliation status */}
        <div>
          <h2 className="text-sm font-medium text-foreground mb-3">
            <div className="flex items-center gap-2">
              <Shield size={14} />
              Reconciliation
            </div>
          </h2>
          {health.isLoading ? (
            <Skeleton className="h-40 rounded-lg" />
          ) : h?.reconciliation ? (
            <div className="rounded-lg border bg-card p-5">
              <div className="flex items-center justify-between mb-4">
                <Badge
                  variant={h.reconciliation.healthy ? "success" : "danger"}
                >
                  {h.reconciliation.healthy
                    ? "All Checks Passing"
                    : "Issues Detected"}
                </Badge>
                <span className="text-xs text-muted-foreground tabular-nums">
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
                    className="rounded-md border bg-background p-3 text-center"
                  >
                    <p
                      className={`text-xl font-semibold tabular-nums ${checkValueColor(check.value)}`}
                    >
                      {check.value}
                    </p>
                    <p className="text-xs text-muted-foreground mt-1">
                      {check.label}
                    </p>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="rounded-lg border bg-card p-5 text-center">
              <p className="text-xs text-muted-foreground">
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
