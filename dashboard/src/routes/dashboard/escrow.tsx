import { useState, useMemo, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { KpiCard } from "@/components/ui/kpi-card";
import { useEscrows } from "@/hooks/api/use-escrow";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { useRealtimeStore } from "@/stores/realtime-store";
import { Address } from "@/components/ui/address";
import type { Escrow } from "@/lib/types";
import { Lock, AlertTriangle, DollarSign, Clock, MoreHorizontal, Eye } from "lucide-react";
import { SkeletonCard } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { PageHeader } from "@/components/layouts/page-header";

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
  const [viewEscrow, setViewEscrow] = useState<Escrow | null>(null);
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
      id: "amount",
      header: "Amount",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.amount) || 0,
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
      sortable: true,
      sortValue: (row) => new Date(row.autoReleaseAt).getTime(),
      cell: (row) => (
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.autoReleaseAt)}
        </span>
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
              <button aria-label="Escrow actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewEscrow(row)}>
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
      <PageHeader icon={Lock} title="Escrow Monitor" description="Active escrow positions and lifecycle tracking" />

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
        {escrows.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={Lock} label="Active Escrows" value={activeCount} />
            <KpiCard icon={AlertTriangle} label="Disputed" value={disputedCount} />
            <KpiCard icon={DollarSign} label="Total Escrowed" value={`$${totalEscrowed.toFixed(2)}`} />
            <KpiCard icon={Clock} label="Total" value={allEscrows.length} />
          </>
        )}
      </div>

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {escrows.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load escrows
            <Button variant="ghost" size="sm" onClick={() => escrows.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={filteredEscrows}
            isLoading={escrows.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewEscrow(row)}
            dataUpdatedAt={escrows.dataUpdatedAt}
            emptyTitle={statusFilter === "all" ? "No escrows" : `No ${statusFilter} escrows`}
            emptyDescription="No escrow records found."
            totalLabel={`${filteredEscrows.length} escrows`}
          />
        )}
      </div>

      <Dialog open={!!viewEscrow} onOpenChange={() => setViewEscrow(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Escrow Details</DialogTitle>
            <DialogDescription>Escrow position and lifecycle information.</DialogDescription>
          </DialogHeader>
          {viewEscrow && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Escrow ID</span>
                  <code className="text-right font-mono text-xs">{viewEscrow.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Buyer</span>
                  <Address value={viewEscrow.buyerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Seller</span>
                  <Address value={viewEscrow.sellerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={(STATUS_VARIANT[viewEscrow.status] ?? "default") as "accent" | "success" | "default" | "warning" | "danger"}>
                    {viewEscrow.status}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Amount</span>
                  <span className="tabular-nums">{formatCurrency(viewEscrow.amount)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Service</span>
                  <code className="font-mono text-xs">{viewEscrow.serviceId}</code>
                </div>
                {viewEscrow.disputeReason && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Dispute Reason</span>
                    <span className="text-right text-xs">{viewEscrow.disputeReason}</span>
                  </div>
                )}
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Auto-release</span>
                  <span>{relativeTime(viewEscrow.autoReleaseAt)}</span>
                </div>
                {viewEscrow.deliveredAt && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Delivered</span>
                    <span>{relativeTime(viewEscrow.deliveredAt)}</span>
                  </div>
                )}
                {viewEscrow.resolvedAt && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Resolved</span>
                    <span>{relativeTime(viewEscrow.resolvedAt)}</span>
                  </div>
                )}
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewEscrow.createdAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewEscrow(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
