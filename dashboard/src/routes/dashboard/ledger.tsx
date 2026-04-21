import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard } from "@/components/ui/skeleton";
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
import { Input } from "@/components/ui/input";
import { Address } from "@/components/ui/address";
import { PageHeader } from "@/components/layouts/page-header";
import { useBalance, useLedgerHistory, useCreditInfo, useWithdraw } from "@/hooks/api/use-ledger";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { ApiError } from "@/lib/api-client";
import type { LedgerEntry } from "@/lib/types";
import {
  Wallet,
  DollarSign,
  ArrowDownLeft,
  ArrowUpRight,
  Lock,
  CreditCard,
  AlertTriangle,
  Loader2,
  MoreHorizontal,
  Eye,
} from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import { toast } from "sonner";

const TYPE_VARIANT: Record<string, string> = {
  deposit: "success",
  withdrawal: "warning",
  spend: "default",
  refund: "accent",
  hold: "warning",
  release: "default",
  credit_draw: "danger",
  credit_repay: "success",
};

const TYPE_ICON: Record<string, typeof ArrowDownLeft> = {
  deposit: ArrowDownLeft,
  withdrawal: ArrowUpRight,
  refund: ArrowDownLeft,
};

export function LedgerPage() {
  const balance = useBalance();
  const credit = useCreditInfo();
  const [cursor, setCursor] = useState<string | undefined>();
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  const history = useLedgerHistory(cursor);
  const withdrawMutation = useWithdraw();

  const [withdrawOpen, setWithdrawOpen] = useState(false);
  const [withdrawAmount, setWithdrawAmount] = useState("");
  const [viewEntry, setViewEntry] = useState<LedgerEntry | null>(null);

  const bal = balance.data?.balance;
  const entries = history.data?.entries ?? [];

  const handleWithdraw = () => {
    withdrawMutation.mutate(withdrawAmount, {
      onSuccess: (data) => {
        setWithdrawOpen(false);
        setWithdrawAmount("");
        toast.success(data.message || "Withdrawal submitted");
        balance.refetch();
        history.refetch();
      },
      onError: (err) => {
        const msg =
          err instanceof ApiError &&
          typeof err.body === "object" &&
          err.body !== null &&
          "message" in err.body
            ? (err.body as { message: string }).message
            : "Withdrawal failed";
        toast.error(msg);
      },
    });
  };

  const handleNextPage = () => {
    if (history.data?.nextCursor) {
      setCursorStack((prev) => [...prev, cursor ?? ""]);
      setCursor(history.data.nextCursor);
    }
  };

  const handlePrevPage = () => {
    setCursorStack((prev) => {
      const next = [...prev];
      const prevCursor = next.pop();
      setCursor(prevCursor || undefined);
      return next;
    });
  };

  const columns: Column<LedgerEntry>[] = [
    {
      id: "type",
      header: "Type",
      cell: (row) => {
        const Icon = TYPE_ICON[row.type] ?? DollarSign;
        return (
          <div className="flex items-center gap-2">
            <Icon size={13} className="shrink-0 text-muted-foreground" />
            <Badge
              variant={
                (TYPE_VARIANT[row.type] ?? "default") as
                  | "success"
                  | "warning"
                  | "default"
                  | "accent"
                  | "danger"
              }
            >
              {row.type}
            </Badge>
          </div>
        );
      },
    },
    {
      id: "amount",
      header: "Amount",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.amount) || 0,
      cell: (row) => {
        const num = parseFloat(row.amount) || 0;
        const isCredit = ["deposit", "refund", "release", "credit_draw"].includes(row.type);
        return (
          <span className={isCredit ? "text-success" : "text-foreground"}>
            {isCredit ? "+" : "-"}
            {formatCurrency(Math.abs(num))}
          </span>
        );
      },
    },
    {
      id: "description",
      header: "Description",
      cell: (row) => (
        <span className="max-w-[200px] truncate text-xs text-muted-foreground">
          {row.description || row.reference || "—"}
        </span>
      ),
    },
    {
      id: "status",
      header: "",
      className: "w-16",
      cell: (row) =>
        row.reversedAt ? (
          <Badge variant="danger">reversed</Badge>
        ) : null,
    },
    {
      id: "created",
      header: "Date",
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
              <button
                aria-label="Entry actions"
                className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              >
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewEntry(row)}>
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
      <PageHeader
        icon={Wallet}
        title="Ledger"
        description="Account balance, transaction history, and withdrawals"
        actions={
          <Button
            variant="primary"
            size="sm"
            onClick={() => setWithdrawOpen(true)}
            disabled={!bal}
          >
            <ArrowUpRight size={14} />
            Withdraw
          </Button>
        }
      />

      {/* Balance KPI cards */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3 border-b px-4 md:px-8 py-4">
        {balance.isLoading ? (
          Array.from({ length: 5 }).map((_, i) => <SkeletonCard key={i} />)
        ) : balance.isError ? (
          <div className="col-span-full flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load balance
            <Button variant="ghost" size="sm" onClick={() => balance.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <>
            <KpiCard
              icon={DollarSign}
              label="Available"
              value={formatCurrency(bal?.available ?? "0")}
            />
            <KpiCard
              icon={Lock}
              label="Escrowed"
              value={formatCurrency(bal?.escrowed ?? "0")}
            />
            <KpiCard
              icon={Wallet}
              label="Pending"
              value={formatCurrency(bal?.pending ?? "0")}
            />
            <KpiCard
              icon={ArrowDownLeft}
              label="Total In"
              value={formatCurrency(bal?.totalIn ?? "0")}
            />
            <KpiCard
              icon={ArrowUpRight}
              label="Total Out"
              value={formatCurrency(bal?.totalOut ?? "0")}
            />
          </>
        )}
      </div>

      {/* Credit info */}
      {credit.data && parseFloat(credit.data.limit) > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 border-b px-4 md:px-8 py-4">
          <KpiCard
            icon={CreditCard}
            label="Credit Limit"
            value={formatCurrency(credit.data.limit)}
          />
          <KpiCard
            icon={CreditCard}
            label="Credit Used"
            value={formatCurrency(credit.data.used)}
            change={
              parseFloat(credit.data.used) > 0
                ? `${((parseFloat(credit.data.used) / parseFloat(credit.data.limit)) * 100).toFixed(0)}% utilization`
                : undefined
            }
            changeType={
              parseFloat(credit.data.used) / parseFloat(credit.data.limit) > 0.8
                ? "negative"
                : "neutral"
            }
          />
          <KpiCard
            icon={CreditCard}
            label="Credit Available"
            value={formatCurrency(credit.data.available)}
          />
        </div>
      )}

      {/* Transaction history */}
      <div className="px-4 md:px-8 py-4">
        {history.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load transactions
            <Button variant="ghost" size="sm" onClick={() => history.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={entries}
            isLoading={history.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewEntry(row)}
            dataUpdatedAt={history.dataUpdatedAt}
            emptyTitle="No transactions"
            emptyDescription="Ledger entries will appear here once you start transacting."
            totalLabel={`${entries.length} entries`}
            hasNextPage={!!history.data?.hasMore}
            hasPrevPage={cursorStack.length > 0}
            onNextPage={handleNextPage}
            onPrevPage={handlePrevPage}
            page={cursorStack.length + 1}
          />
        )}
      </div>

      {/* Entry detail dialog */}
      <Dialog open={!!viewEntry} onOpenChange={() => setViewEntry(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Ledger Entry</DialogTitle>
            <DialogDescription>Transaction details</DialogDescription>
          </DialogHeader>
          {viewEntry && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Entry ID</span>
                  <code className="text-right font-mono text-xs">{viewEntry.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Agent</span>
                  <Address value={viewEntry.agentAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Type</span>
                  <Badge
                    variant={
                      (TYPE_VARIANT[viewEntry.type] ?? "default") as
                        | "success"
                        | "warning"
                        | "default"
                        | "accent"
                        | "danger"
                    }
                  >
                    {viewEntry.type}
                  </Badge>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Amount</span>
                  <span className="tabular-nums">{formatCurrency(viewEntry.amount)}</span>
                </div>
                <hr className="border-border" />
                {viewEntry.description && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Description</span>
                    <span className="text-right text-xs">{viewEntry.description}</span>
                  </div>
                )}
                {viewEntry.reference && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Reference</span>
                    <code className="text-right font-mono text-xs">{viewEntry.reference}</code>
                  </div>
                )}
                {viewEntry.txHash && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Tx Hash</span>
                    <code className="text-right font-mono text-xs">
                      {viewEntry.txHash.slice(0, 16)}...
                    </code>
                  </div>
                )}
                {viewEntry.reversedAt && (
                  <>
                    <hr className="border-border" />
                    <div className="flex items-start justify-between gap-4">
                      <span className="text-xs text-muted-foreground">Reversed</span>
                      <span className="text-xs text-destructive">
                        {relativeTime(viewEntry.reversedAt)}
                      </span>
                    </div>
                    {viewEntry.reversedBy && (
                      <div className="flex items-start justify-between gap-4">
                        <span className="text-xs text-muted-foreground">Reversed By</span>
                        <code className="text-right font-mono text-xs">{viewEntry.reversedBy}</code>
                      </div>
                    )}
                  </>
                )}
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewEntry.createdAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewEntry(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Withdraw dialog */}
      <Dialog open={withdrawOpen} onOpenChange={setWithdrawOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Withdraw Funds</DialogTitle>
            <DialogDescription>
              Request a withdrawal from your available balance
              {bal && (
                <> ({formatCurrency(bal.available)} available)</>
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <Input
              id="withdraw-amount"
              label="Amount (USDC)"
              type="number"
              step="0.01"
              min="0.01"
              placeholder="0.00"
              value={withdrawAmount}
              onChange={(e) => setWithdrawAmount(e.target.value)}
              autoFocus
            />
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setWithdrawOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={handleWithdraw}
              disabled={
                withdrawMutation.isPending ||
                !withdrawAmount ||
                parseFloat(withdrawAmount) <= 0
              }
            >
              {withdrawMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Processing...
                </>
              ) : (
                "Withdraw"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
