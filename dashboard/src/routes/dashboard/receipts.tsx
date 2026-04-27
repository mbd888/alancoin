import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogBody,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Tabs } from "@/components/ui/tabs";
import { Address } from "@/components/ui/address";
import { PageHeader } from "@/components/layouts/page-header";
import { useReceipts, useVerifyReceipt } from "@/hooks/api/use-receipts";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { Receipt } from "@/lib/types";
import {
  FileCheck,
  AlertTriangle,
  MoreHorizontal,
  Eye,
  ShieldCheck,
  Loader2,
  CheckCircle,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { useMemo } from "react";

const STATUS_VARIANT: Record<string, string> = {
  confirmed: "success",
  failed: "danger",
};

const PATH_VARIANT: Record<string, string> = {
  gateway: "accent",
  escrow: "warning",
  stream: "default",
  session_key: "default",
};

const PATH_TABS = [
  { id: "all", label: "All" },
  { id: "gateway", label: "Gateway" },
  { id: "escrow", label: "Escrow" },
  { id: "stream", label: "Stream" },
  { id: "session_key", label: "Session Key" },
];

export function ReceiptsPage() {
  const [pathFilter, setPathFilter] = useState("all");
  const [viewReceipt, setViewReceipt] = useState<Receipt | null>(null);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const receipts = useReceipts();
  const verifyMutation = useVerifyReceipt();

  const allReceipts = receipts.data?.receipts ?? [];

  const filteredReceipts = useMemo(() => {
    if (pathFilter === "all") return allReceipts;
    return allReceipts.filter((r) => r.paymentPath === pathFilter);
  }, [allReceipts, pathFilter]);

  const counts = useMemo(() => {
    const map: Record<string, number> = { all: allReceipts.length };
    for (const r of allReceipts) {
      map[r.paymentPath] = (map[r.paymentPath] ?? 0) + 1;
    }
    return map;
  }, [allReceipts]);

  const tabsWithCounts = PATH_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  const handleVerify = (receipt: Receipt) => {
    setVerifyingId(receipt.id);
    verifyMutation.mutate(receipt.id, {
      onSuccess: (data) => {
        setVerifyingId(null);
        if (data.verification.valid) {
          toast.success("Receipt verified — signature is valid");
        } else {
          toast.error(
            data.verification.error || "Verification failed — signature invalid"
          );
        }
      },
      onError: () => {
        setVerifyingId(null);
        toast.error("Failed to verify receipt");
      },
    });
  };

  const columns: Column<Receipt>[] = [
    {
      id: "id",
      header: "Receipt",
      cell: (row) => (
        <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "path",
      header: "Path",
      cell: (row) => (
        <Badge
          variant={
            (PATH_VARIANT[row.paymentPath] ?? "default") as
              | "accent"
              | "warning"
              | "default"
          }
        >
          {row.paymentPath.replace(/_/g, " ")}
        </Badge>
      ),
    },
    {
      id: "from",
      header: "From",
      cell: (row) => <Address value={row.from} />,
    },
    {
      id: "to",
      header: "To",
      cell: (row) => <Address value={row.to} />,
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
        <Badge
          variant={
            (STATUS_VARIANT[row.status] ?? "default") as "success" | "danger" | "default"
          }
        >
          {row.status}
        </Badge>
      ),
    },
    {
      id: "chain",
      header: "#",
      numeric: true,
      sortable: true,
      sortValue: (row) => row.chainIndex,
      cell: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.chainIndex}
        </span>
      ),
    },
    {
      id: "issued",
      header: "Issued",
      sortable: true,
      sortValue: (row) => new Date(row.issuedAt).getTime(),
      cell: (row) => (
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.issuedAt)}
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
              <button
                aria-label="Receipt actions"
                className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              >
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewReceipt(row)}>
                <Eye size={13} />
                View details
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() => handleVerify(row)}
                disabled={verifyingId === row.id}
              >
                {verifyingId === row.id ? (
                  <Loader2 size={13} className="animate-spin" />
                ) : (
                  <ShieldCheck size={13} />
                )}
                Verify signature
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={FileCheck}
        title="Receipts"
        description="Cryptographic payment proofs with hash-chain integrity"
      />

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={pathFilter} onChange={setPathFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {receipts.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load receipts
            <Button variant="ghost" size="sm" onClick={() => receipts.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={filteredReceipts}
            isLoading={receipts.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewReceipt(row)}
            dataUpdatedAt={receipts.dataUpdatedAt}
            emptyTitle={
              pathFilter === "all"
                ? "No receipts"
                : `No ${pathFilter.replace(/_/g, " ")} receipts`
            }
            emptyDescription="Payment receipts will appear here after transactions settle."
            totalLabel={`${filteredReceipts.length} receipts`}
          />
        )}
      </div>

      {/* Receipt detail dialog */}
      <Dialog open={!!viewReceipt} onOpenChange={() => setViewReceipt(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Receipt Details</DialogTitle>
            <DialogDescription>
              Cryptographic payment proof with chain verification.
            </DialogDescription>
          </DialogHeader>
          {viewReceipt && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Receipt ID</span>
                  <code className="text-right font-mono text-xs break-all">
                    {viewReceipt.id}
                  </code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge
                    variant={
                      (STATUS_VARIANT[viewReceipt.status] ?? "default") as
                        | "success"
                        | "danger"
                        | "default"
                    }
                  >
                    {viewReceipt.status}
                  </Badge>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Payment Path</span>
                  <Badge
                    variant={
                      (PATH_VARIANT[viewReceipt.paymentPath] ?? "default") as
                        | "accent"
                        | "warning"
                        | "default"
                    }
                  >
                    {viewReceipt.paymentPath.replace(/_/g, " ")}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">From</span>
                  <Address value={viewReceipt.from} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">To</span>
                  <Address value={viewReceipt.to} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Amount</span>
                  <span className="tabular-nums">{formatCurrency(viewReceipt.amount)}</span>
                </div>
                {viewReceipt.serviceId && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Service</span>
                    <code className="text-right font-mono text-xs">
                      {viewReceipt.serviceId}
                    </code>
                  </div>
                )}
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Chain Index</span>
                  <span className="font-mono text-xs tabular-nums">
                    {viewReceipt.chainIndex}
                  </span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Scope</span>
                  <code className="text-right font-mono text-xs">{viewReceipt.scope}</code>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs text-muted-foreground">Payload Hash</span>
                  <code className="break-all rounded-md border bg-background px-2 py-1 font-mono text-[10px] text-muted-foreground">
                    {viewReceipt.payloadHash}
                  </code>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs text-muted-foreground">Signature</span>
                  <code className="break-all rounded-md border bg-background px-2 py-1 font-mono text-[10px] text-muted-foreground">
                    {viewReceipt.signature}
                  </code>
                </div>
                {viewReceipt.prevHash && (
                  <div className="flex flex-col gap-1">
                    <span className="text-xs text-muted-foreground">Previous Hash</span>
                    <code className="break-all rounded-md border bg-background px-2 py-1 font-mono text-[10px] text-muted-foreground">
                      {viewReceipt.prevHash}
                    </code>
                  </div>
                )}
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Issued</span>
                  <span>{relativeTime(viewReceipt.issuedAt)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Expires</span>
                  <span>{relativeTime(viewReceipt.expiresAt)}</span>
                </div>

                {/* Inline verify button */}
                <div className="mt-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    className="w-full"
                    onClick={() => handleVerify(viewReceipt)}
                    disabled={verifyMutation.isPending}
                  >
                    {verifyMutation.isPending ? (
                      <>
                        <Loader2 size={14} className="animate-spin" />
                        Verifying...
                      </>
                    ) : verifyMutation.data?.verification.receiptId ===
                      viewReceipt.id ? (
                      verifyMutation.data.verification.valid ? (
                        <>
                          <CheckCircle size={14} className="text-success" />
                          Signature Valid
                        </>
                      ) : (
                        <>
                          <XCircle size={14} className="text-destructive" />
                          Invalid — {verifyMutation.data.verification.error}
                        </>
                      )
                    ) : (
                      <>
                        <ShieldCheck size={14} />
                        Verify Signature
                      </>
                    )}
                  </Button>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewReceipt(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
