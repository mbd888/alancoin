import { useState, useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { KpiCard } from "@/components/ui/kpi-card";
import { useStreams } from "@/hooks/api/use-streams";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { StreamItem } from "@/lib/types";
import { Radio, DollarSign, Hash, Activity } from "lucide-react";

const STATUS_VARIANT: Record<string, string> = {
  open: "accent",
  settled: "success",
  closed: "default",
};

const STATUS_TABS = [
  { id: "all", label: "All" },
  { id: "open", label: "Open" },
  { id: "settled", label: "Settled" },
  { id: "closed", label: "Closed" },
];

export function StreamsPage() {
  const [statusFilter, setStatusFilter] = useState("all");
  const streams = useStreams();

  const allStreams = streams.data?.streams ?? [];

  const filteredStreams = useMemo(() => {
    if (statusFilter === "all") return allStreams;
    return allStreams.filter((s) => s.status === statusFilter);
  }, [allStreams, statusFilter]);

  const counts = useMemo(() => {
    const map: Record<string, number> = { all: allStreams.length };
    for (const s of allStreams) {
      map[s.status] = (map[s.status] ?? 0) + 1;
    }
    return map;
  }, [allStreams]);

  const activeCount = counts.open ?? 0;
  const totalTicks = allStreams.reduce((sum, s) => sum + s.tickCount, 0);
  const totalSpent = allStreams.reduce(
    (sum, s) => sum + (parseFloat(s.spentAmount) || 0),
    0
  );

  const tabsWithCounts = STATUS_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  const columns: Column<StreamItem>[] = [
    {
      id: "id",
      header: "Stream",
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
      id: "spent",
      header: "Spent / Held",
      numeric: true,
      cell: (row) => (
        <span className="text-[12px]">
          {formatCurrency(row.spentAmount)}
          <span className="text-[var(--foreground-disabled)]"> / </span>
          {formatCurrency(row.holdAmount)}
        </span>
      ),
    },
    {
      id: "ticks",
      header: "Ticks",
      numeric: true,
      cell: (row) => row.tickCount,
    },
    {
      id: "price",
      header: "$/Tick",
      numeric: true,
      cell: (row) => formatCurrency(row.pricePerTick),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge variant={(STATUS_VARIANT[row.status] ?? "default") as "accent" | "success" | "default"}>
          {row.status}
        </Badge>
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
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Streams</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Streaming micropayment channels
        </p>
      </header>

      <div className="grid grid-cols-3 gap-4 border-b border-[var(--border)] px-8 py-4">
        <KpiCard icon={Radio} label="Active Streams" value={activeCount} />
        <KpiCard icon={Hash} label="Total Ticks" value={totalTicks} />
        <KpiCard icon={DollarSign} label="Total Spent" value={`$${totalSpent.toFixed(2)}`} />
      </div>

      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={filteredStreams}
          isLoading={streams.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle={statusFilter === "all" ? "No streams" : `No ${statusFilter} streams`}
          emptyDescription="No streaming micropayment channels found."
          totalLabel={`${filteredStreams.length} streams`}
        />
      </div>
    </div>
  );
}
