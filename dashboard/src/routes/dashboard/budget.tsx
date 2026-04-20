import { useState, useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { KpiCard } from "@/components/ui/kpi-card";
import { useSessions } from "@/hooks/api/use-dashboard";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { GatewaySession } from "@/lib/types";
import { Wallet, TrendingUp, AlertTriangle, Layers, MoreHorizontal, Eye } from "lucide-react";
import { SkeletonCard } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { PageHeader } from "@/components/layouts/page-header";
import { Address } from "@/components/ui/address";

export function BudgetPage() {
  const [viewSession, setViewSession] = useState<GatewaySession | null>(null);
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
      cell: (row) => <Address value={row.agentAddr} />,
    },
    {
      id: "budget",
      header: "Budget Usage",
      numeric: true,
      sortable: true,
      sortValue: (row) => {
        const spent = parseFloat(row.totalSpent) || 0;
        const total = parseFloat(row.maxTotal) || 1;
        return spent / total;
      },
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
      sortable: true,
      sortValue: (row) => parseFloat(row.totalSpent) || 0,
      cell: (row) => (
        <span className="text-xs">{formatCurrency(row.totalSpent)}</span>
      ),
    },
    {
      id: "total",
      header: "Budget",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.maxTotal) || 0,
      cell: (row) => (
        <span className="text-xs">{formatCurrency(row.maxTotal)}</span>
      ),
    },
    {
      id: "remaining",
      header: "Remaining",
      numeric: true,
      sortable: true,
      sortValue: (row) => (parseFloat(row.maxTotal) || 0) - (parseFloat(row.totalSpent) || 0),
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
      sortable: true,
      sortValue: (row) => row.requestCount,
      cell: (row) => row.requestCount,
    },
    {
      id: "actions",
      header: "",
      className: "w-10",
      cell: (row) => (
        <div onClick={(e) => e.stopPropagation()}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button aria-label="Session actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewSession(row)}>
                <Eye size={13} />
                View details
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <PageHeader icon={Wallet} title="Budget Tracker" description="Active session budget utilization" />

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
            onRowClick={(row) => setViewSession(row)}
            dataUpdatedAt={sessions.dataUpdatedAt}
            emptyTitle="No active sessions"
            emptyDescription="No gateway sessions with active budgets."
            totalLabel={`${activeSessions.length} active sessions`}
          />
        )}
      </div>

      <Dialog open={!!viewSession} onOpenChange={() => setViewSession(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Budget Details</DialogTitle>
            <DialogDescription>Session budget utilization breakdown.</DialogDescription>
          </DialogHeader>
          {viewSession && (() => {
            const spent = parseFloat(viewSession.totalSpent) || 0;
            const total = parseFloat(viewSession.maxTotal) || 1;
            const pct = Math.min((spent / total) * 100, 100);
            const remaining = total - spent;
            return (
              <DialogBody>
                <div className="flex flex-col gap-3 text-sm">
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Session ID</span>
                    <code className="text-right font-mono text-xs">{viewSession.id}</code>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Agent</span>
                    <Address value={viewSession.agentAddr} truncate={false} />
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Strategy</span>
                    <span>{viewSession.strategy}</span>
                  </div>
                  <hr className="border-border" />
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Utilization</span>
                    <div className="flex items-center gap-2">
                      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full"
                          style={{
                            width: `${pct}%`,
                            backgroundColor: pct >= 90 ? "var(--color-danger)" : pct >= 70 ? "var(--color-warning)" : "var(--color-accent-6)",
                          }}
                        />
                      </div>
                      <span className="tabular-nums">{pct.toFixed(1)}%</span>
                    </div>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Spent</span>
                    <span className="tabular-nums">{formatCurrency(viewSession.totalSpent)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Budget</span>
                    <span className="tabular-nums">{formatCurrency(viewSession.maxTotal)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Remaining</span>
                    <span className="tabular-nums" style={{ color: remaining < 1 ? "var(--color-danger)" : undefined }}>
                      {formatCurrency(remaining.toFixed(6))}
                    </span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Max Per Request</span>
                    <span className="tabular-nums">{formatCurrency(viewSession.maxPerRequest)}</span>
                  </div>
                  <hr className="border-border" />
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Requests</span>
                    <span className="tabular-nums">{viewSession.requestCount}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Expires</span>
                    <span>{relativeTime(viewSession.expiresAt)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Created</span>
                    <span>{relativeTime(viewSession.createdAt)}</span>
                  </div>
                </div>
              </DialogBody>
            );
          })()}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewSession(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
