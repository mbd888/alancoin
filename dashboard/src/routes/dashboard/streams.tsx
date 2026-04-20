import { useState, useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { useStreams } from "@/hooks/api/use-streams";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { Address } from "@/components/ui/address";
import type { StreamItem } from "@/lib/types";
import { Radio, DollarSign, Hash, Zap, AlertTriangle, MoreHorizontal, Eye } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";

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
  const [viewStream, setViewStream] = useState<StreamItem | null>(null);
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
        <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "buyer",
      header: "Buyer",
      cell: (row) => <Address value={row.buyerAddr} />,
    },
    {
      id: "seller",
      header: "Seller",
      cell: (row) => <Address value={row.sellerAddr} />,
    },
    {
      id: "spent",
      header: "Spent / Held",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.spentAmount) || 0,
      cell: (row) => (
        <span className="text-xs">
          {formatCurrency(row.spentAmount)}
          <span className="text-muted-foreground/50"> / </span>
          {formatCurrency(row.holdAmount)}
        </span>
      ),
    },
    {
      id: "ticks",
      header: "Ticks",
      numeric: true,
      sortable: true,
      sortValue: (row) => row.tickCount,
      cell: (row) => row.tickCount,
    },
    {
      id: "price",
      header: "$/Tick",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.pricePerTick) || 0,
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
      sortable: true,
      sortValue: (row) => new Date(row.createdAt).getTime(),
      cell: (row) => (
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      className: "w-10",
      cell: (row) => (
        <div onClick={(e) => e.stopPropagation()}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button aria-label="Stream actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewStream(row)}>
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
      <PageHeader icon={Zap} title="Streams" description="Streaming micropayment channels" />

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 border-b px-4 md:px-8 py-4">
        {streams.isLoading ? (
          Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={Radio} label="Active Streams" value={activeCount} />
            <KpiCard icon={Hash} label="Total Ticks" value={totalTicks} />
            <KpiCard icon={DollarSign} label="Total Spent" value={`$${totalSpent.toFixed(2)}`} />
          </>
        )}
      </div>

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {streams.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load streams
            <Button variant="ghost" size="sm" onClick={() => streams.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={filteredStreams}
            isLoading={streams.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewStream(row)}
            dataUpdatedAt={streams.dataUpdatedAt}
            emptyTitle={statusFilter === "all" ? "No streams" : `No ${statusFilter} streams`}
            emptyDescription="No streaming micropayment channels found."
            totalLabel={`${filteredStreams.length} streams`}
          />
        )}
      </div>

      <Dialog open={!!viewStream} onOpenChange={() => setViewStream(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Stream Details</DialogTitle>
            <DialogDescription>Streaming micropayment channel information.</DialogDescription>
          </DialogHeader>
          {viewStream && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Stream ID</span>
                  <code className="text-right font-mono text-xs">{viewStream.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Buyer</span>
                  <Address value={viewStream.buyerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Seller</span>
                  <Address value={viewStream.sellerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={(STATUS_VARIANT[viewStream.status] ?? "default") as "accent" | "success" | "default"}>
                    {viewStream.status}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Hold Amount</span>
                  <span className="tabular-nums">{formatCurrency(viewStream.holdAmount)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Spent</span>
                  <span className="tabular-nums">{formatCurrency(viewStream.spentAmount)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Price / Tick</span>
                  <span className="tabular-nums">{formatCurrency(viewStream.pricePerTick)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Tick Count</span>
                  <span className="tabular-nums">{viewStream.tickCount}</span>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewStream.createdAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewStream(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
