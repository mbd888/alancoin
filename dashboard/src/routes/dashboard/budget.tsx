import { useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { KpiCard } from "@/components/ui/kpi-card";
import { useSessions } from "@/hooks/api/use-dashboard";
import { formatCurrency } from "@/lib/utils";
import type { GatewaySession } from "@/lib/types";
import { Wallet, TrendingUp, AlertTriangle, Layers } from "lucide-react";

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
        <span className="font-mono text-[12px]">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "agent",
      header: "Agent",
      cell: (row) => (
        <span className="font-mono text-[12px]">
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
            <div className="h-1.5 w-20 overflow-hidden rounded-full bg-[var(--color-gray-3)]">
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
            <span className="text-[12px]">{pct.toFixed(0)}%</span>
          </div>
        );
      },
    },
    {
      id: "spent",
      header: "Spent",
      numeric: true,
      cell: (row) => (
        <span className="text-[12px]">{formatCurrency(row.totalSpent)}</span>
      ),
    },
    {
      id: "total",
      header: "Budget",
      numeric: true,
      cell: (row) => (
        <span className="text-[12px]">{formatCurrency(row.maxTotal)}</span>
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
            className="text-[12px]"
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
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Budget Tracker</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Active session budget utilization
        </p>
      </header>

      {/* KPI cards */}
      <div className="grid grid-cols-4 gap-4 border-b border-[var(--border)] px-8 py-4">
        <KpiCard icon={Wallet} label="Total Budget" value={`$${totalBudget.toFixed(2)}`} />
        <KpiCard icon={TrendingUp} label="Total Spent" value={`$${totalSpent.toFixed(2)}`} />
        <KpiCard icon={Layers} label="Utilization" value={`${utilization.toFixed(1)}%`} />
        <KpiCard icon={AlertTriangle} label="Near Exhaustion" value={nearExhaustion} />
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={activeSessions}
          isLoading={sessions.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle="No active sessions"
          emptyDescription="No gateway sessions with active budgets."
          totalLabel={`${activeSessions.length} active sessions`}
        />
      </div>
    </div>
  );
}
