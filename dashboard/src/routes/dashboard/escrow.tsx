import { useState, useMemo, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { KpiCard } from "@/components/ui/kpi-card";
import { useEscrows } from "@/hooks/api/use-escrow";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { useRealtimeStore } from "@/stores/realtime-store";
import type { Escrow } from "@/lib/types";
import { Lock, AlertTriangle, DollarSign, Clock } from "lucide-react";

const STATUS_VARIANT: Record<string, string> = {
  pending: "warning",
  delivered: "accent",
  released: "success",
  refunded: "default",
  expired: "default",
  disputed: "danger",
  arbitrating: "danger",
};

const STATUS_TABS = [
  { id: "all", label: "All" },
  { id: "pending", label: "Pending" },
  { id: "delivered", label: "Delivered" },
  { id: "disputed", label: "Disputed" },
  { id: "released", label: "Released" },
  { id: "refunded", label: "Refunded" },
];

export function EscrowPage() {
  const [statusFilter, setStatusFilter] = useState("all");
  const escrows = useEscrows();
  const queryClient = useQueryClient();
  const { events, connect, disconnect } = useRealtimeStore();

  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  useEffect(() => {
    const last = events[0];
    if (last && last.type.startsWith("escrow_")) {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "escrows"] });
    }
  }, [events, queryClient]);

  const allEscrows = escrows.data?.escrows ?? [];

  const filteredEscrows = useMemo(() => {
    if (statusFilter === "all") return allEscrows;
    return allEscrows.filter((e) => e.status === statusFilter);
  }, [allEscrows, statusFilter]);

  const counts = useMemo(() => {
    const map: Record<string, number> = { all: allEscrows.length };
    for (const e of allEscrows) {
      map[e.status] = (map[e.status] ?? 0) + 1;
    }
    return map;
  }, [allEscrows]);

  const activeCount = (counts.pending ?? 0) + (counts.delivered ?? 0);
  const disputedCount = (counts.disputed ?? 0) + (counts.arbitrating ?? 0);
  const totalEscrowed = allEscrows
    .filter((e) => !["released", "refunded", "expired"].includes(e.status))
    .reduce((sum, e) => sum + (parseFloat(e.amount) || 0), 0);

  const tabsWithCounts = STATUS_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  const columns: Column<Escrow>[] = [
    {
      id: "id",
      header: "Escrow",
      cell: (row) => (
        <span className="font-mono text-[12px]">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "buyer",
      header: "Buyer",
      cell: (row) => (
        <span className="font-mono text-[12px]">
          {row.buyerAddr.slice(0, 8)}...{row.buyerAddr.slice(-4)}
        </span>
      ),
    },
    {
      id: "seller",
      header: "Seller",
      cell: (row) => (
        <span className="font-mono text-[12px]">
          {row.sellerAddr.slice(0, 8)}...{row.sellerAddr.slice(-4)}
        </span>
      ),
    },
    {
      id: "amount",
      header: "Amount",
      numeric: true,
      cell: (row) => formatCurrency(row.amount),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge variant={(STATUS_VARIANT[row.status] ?? "default") as "accent" | "success" | "default" | "warning" | "danger"}>
          {row.status}
        </Badge>
      ),
    },
    {
      id: "autoRelease",
      header: "Auto-release",
      cell: (row) => (
        <span className="text-[12px] text-[var(--foreground-muted)]">
          {relativeTime(row.autoReleaseAt)}
        </span>
      ),
    },
    {
      id: "created",
      header: "Created",
      cell: (row) => (
        <span className="text-[12px] text-[var(--foreground-muted)]">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Escrow Monitor</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Active escrow positions and lifecycle tracking
        </p>
      </header>

      {/* KPI cards */}
      <div className="grid grid-cols-4 gap-4 border-b border-[var(--border)] px-8 py-4">
        <KpiCard icon={Lock} label="Active Escrows" value={activeCount} />
        <KpiCard icon={AlertTriangle} label="Disputed" value={disputedCount} />
        <KpiCard icon={DollarSign} label="Total Escrowed" value={`$${totalEscrowed.toFixed(2)}`} />
        <KpiCard icon={Clock} label="Total" value={allEscrows.length} />
      </div>

      {/* Status tabs */}
      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={filteredEscrows}
          isLoading={escrows.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle={statusFilter === "all" ? "No escrows" : `No ${statusFilter} escrows`}
          emptyDescription="No escrow records found."
          totalLabel={`${filteredEscrows.length} escrows`}
        />
      </div>
    </div>
  );
}
