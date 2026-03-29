import { useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { KpiCard } from "@/components/ui/kpi-card";
import { useSessions } from "@/hooks/api/use-dashboard";
import { formatCurrency } from "@/lib/utils";
import type { GatewaySession } from "@/lib/types";
import { Wallet, TrendingUp, AlertTriangle, Layers } from "lucide-react";
import { SkeletonCard } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/layouts/page-header";

export function BudgetPage() {
  const sessions = useSessions(100);
  const allSessions = sessions.data?.sessions ?? [];

  const activeSessions = useMemo(
    () => allSessions.filter((s) => s.status === "active"),
    [allSessions]
  );

  const totalBudget = activeSessions.reduce(
    (sum, s) => sum + (parseFloat(s.maxTotal) || 0),
    0
  );
  const totalSpent = activeSessions.reduce(
    (sum, s) => sum + (parseFloat(s.totalSpent) || 0),
    0
  );
  const utilization = totalBudget > 0 ? (totalSpent / totalBudget) * 100 : 0;
  const nearExhaustion = activeSessions.filter((s) => {
    const spent = parseFloat(s.totalSpent) || 0;
    const total = parseFloat(s.maxTotal) || 1;
    return spent / total >= 0.8;
  }).length;

  const columns: Column<GatewaySession>[] = [
    {
      id: "id",
      header: "Session",
      cell: (row) => (
        <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "agent",
      header: "Agent",
      cell: (row) => (
        <span className="font-mono text-xs">
          {row.agentAddr.slice(0, 8)}...{row.agentAddr.slice(-4)}
        </span>
      ),
    },
    {
      id: "budget",
      header: "Budget Usage",
      numeric: true,
      cell: (row) => {
        const spent = parseFloat(row.totalSpent) || 0;
        const total = parseFloat(row.maxTotal) || 1;
        const pct = Math.min((spent / total) * 100, 100);
        return (
          <div className="flex items-center gap-3">
            <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full transition-[width] duration-300"
                style={{
                  width: `${pct}%`,
                  backgroundColor:
                    pct >= 90
                      ? "var(--color-danger)"
                      : pct >= 70
                        ? "var(--color-warning)"
                        : "var(--color-accent-6)",
                }}
              />
            </div>
            <span className="text-xs">{pct.toFixed(0)}%</span>
          </div>
        );
      },
    },
    {
      id: "spent",
      header: "Spent",
      numeric: true,
      cell: (row) => (
        <span className="text-xs">{formatCurrency(row.totalSpent)}</span>
      ),
    },
    {
      id: "total",
      header: "Budget",
      numeric: true,
      cell: (row) => (
        <span className="text-xs">{formatCurrency(row.maxTotal)}</span>
      ),
    },
    {
      id: "remaining",
      header: "Remaining",
      numeric: true,
      cell: (row) => {
        const remaining =
          (parseFloat(row.maxTotal) || 0) - (parseFloat(row.totalSpent) || 0);
        return (
          <span
            className="text-xs"
            style={{
              color: remaining < 1 ? "var(--color-danger)" : "var(--foreground-muted)",
            }}
          >
            {formatCurrency(remaining.toFixed(6))}
          </span>
        );
      },
    },
    {
      id: "requests",
      header: "Reqs",
      numeric: true,
      cell: (row) => row.requestCount,
    },
  ];

  return (
    <div className="min-h-screen">
      <PageHeader icon={Wallet} title="Budget Tracker" description="Active session budget utilization" />

      {/* KPI cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
        {sessions.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={Wallet} label="Total Budget" value={`$${totalBudget.toFixed(2)}`} />
            <KpiCard icon={TrendingUp} label="Total Spent" value={`$${totalSpent.toFixed(2)}`} />
            <KpiCard icon={Layers} label="Utilization" value={`${utilization.toFixed(1)}%`} />
            <KpiCard icon={AlertTriangle} label="Near Exhaustion" value={nearExhaustion} />
          </>
        )}
      </div>

      <div className="px-4 md:px-8 py-4">
        {sessions.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load sessions
            <Button variant="ghost" size="sm" onClick={() => sessions.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={activeSessions}
            isLoading={sessions.isLoading}
            keyExtractor={(row) => row.id}
            emptyTitle="No active sessions"
            emptyDescription="No gateway sessions with active budgets."
            totalLabel={`${activeSessions.length} active sessions`}
          />
        )}
      </div>
    </div>
  );
}
