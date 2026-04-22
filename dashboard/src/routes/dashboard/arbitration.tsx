import { useState, useMemo } from "react";
import { Scale, AlertTriangle, CheckCircle, Clock, UserCheck, Zap, Eye, MoreHorizontal } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton, SkeletonCard } from "@/components/ui/skeleton";
import { KpiCard } from "@/components/ui/kpi-card";
import { EmptyState } from "@/components/ui/empty-state";
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
} from "@/components/ui/dropdown-menu";
import { Address } from "@/components/ui/address";
import { useArbitrationCases } from "@/hooks/api/use-arbitration";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { ArbitrationCase } from "@/lib/types";

const STATUS_TABS = [
  { id: "all", label: "All" },
  { id: "open", label: "Open" },
  { id: "assigned", label: "Assigned" },
  { id: "auto_resolved", label: "Auto-Resolved" },
  { id: "resolved", label: "Resolved" },
];

const STATUS_CONFIG: Record<string, { variant: "default" | "success" | "warning" | "accent" | "danger"; label: string }> = {
  open: { variant: "warning", label: "Open" },
  assigned: { variant: "accent", label: "Assigned" },
  auto_resolved: { variant: "success", label: "Auto-Resolved" },
  resolved: { variant: "success", label: "Resolved" },
  appealed: { variant: "danger", label: "Appealed" },
};

const OUTCOME_CONFIG: Record<string, { variant: "default" | "success" | "warning" | "accent" | "danger"; label: string }> = {
  buyer_wins: { variant: "accent", label: "Buyer Wins" },
  seller_wins: { variant: "success", label: "Seller Wins" },
  split: { variant: "warning", label: "Split" },
};

export function ArbitrationPage() {
  const [statusFilter, setStatusFilter] = useState("all");
  const [viewCase, setViewCase] = useState<ArbitrationCase | null>(null);
  const cases = useArbitrationCases(100);

  const allCases = cases.data?.cases ?? [];

  const filteredCases = useMemo(() => {
    if (statusFilter === "all") return allCases;
    return allCases.filter((c) => c.status === statusFilter);
  }, [allCases, statusFilter]);

  const counts = useMemo(() => {
    const m: Record<string, number> = { all: allCases.length };
    for (const c of allCases) {
      m[c.status] = (m[c.status] ?? 0) + 1;
    }
    return m;
  }, [allCases]);

  const totalDisputed = useMemo(
    () => allCases.reduce((sum, c) => sum + (parseFloat(c.disputedAmount) || 0), 0),
    [allCases],
  );

  const tabsWithCounts = STATUS_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={Scale}
        title="Arbitration"
        description="Dispute resolution for escrowed transactions"
      />

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
        {cases.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={Scale} label="Total Cases" value={allCases.length} />
            <KpiCard icon={Clock} label="Open" value={counts.open ?? 0} />
            <KpiCard icon={Zap} label="Auto-Resolved" value={counts.auto_resolved ?? 0} />
            <KpiCard icon={CheckCircle} label="Total Disputed" value={formatCurrency(totalDisputed.toFixed(6))} />
          </>
        )}
      </div>

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {cases.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : cases.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load arbitration cases
            <Button variant="ghost" size="sm" onClick={() => cases.refetch()}>
              Retry
            </Button>
          </div>
        ) : filteredCases.length === 0 ? (
          <EmptyState
            icon={Scale}
            title={statusFilter === "all" ? "No cases" : `No ${statusFilter.replace(/_/g, " ")} cases`}
            description="Arbitration cases are created when escrow disputes are filed."
          />
        ) : (
          <div className="flex flex-col gap-2">
            {filteredCases.map((arbCase) => {
              const statusCfg = STATUS_CONFIG[arbCase.status] ?? { variant: "default" as const, label: arbCase.status };
              const outcomeCfg = arbCase.outcome ? OUTCOME_CONFIG[arbCase.outcome] : null;
              return (
                <div
                  key={arbCase.id}
                  className="flex cursor-pointer items-start gap-4 rounded-lg border bg-card px-5 py-4 transition-colors hover:bg-accent/30"
                  onClick={() => setViewCase(arbCase)}
                >
                  <Scale size={16} strokeWidth={1.8} className="mt-0.5 shrink-0 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <Badge variant={statusCfg.variant}>{statusCfg.label}</Badge>
                      {outcomeCfg && <Badge variant={outcomeCfg.variant}>{outcomeCfg.label}</Badge>}
                      {arbCase.autoResolvable && <Badge variant="default">auto</Badge>}
                      <span className="font-mono text-xs text-muted-foreground">
                        {arbCase.id.slice(0, 16)}...
                      </span>
                      <span className="text-xs text-muted-foreground/50">
                        {relativeTime(arbCase.filedAt)}
                      </span>
                    </div>
                    <p className="mt-1 text-sm text-muted-foreground line-clamp-1">
                      {arbCase.reason}
                    </p>
                    <div className="mt-2 flex items-center gap-4 text-xs text-muted-foreground">
                      <span className="font-medium text-foreground tabular-nums">
                        {formatCurrency(arbCase.disputedAmount)}
                      </span>
                      <span>
                        Buyer: <Address value={arbCase.buyerAddr} />
                      </span>
                      <span>
                        Seller: <Address value={arbCase.sellerAddr} />
                      </span>
                      {arbCase.evidence && arbCase.evidence.length > 0 && (
                        <span>{arbCase.evidence.length} evidence</span>
                      )}
                    </div>
                  </div>
                  <div onClick={(e) => e.stopPropagation()}>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button
                          aria-label="Case actions"
                          className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                        >
                          <MoreHorizontal size={15} />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setViewCase(arbCase)}>
                          <Eye size={13} />
                          View details
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <Dialog open={!!viewCase} onOpenChange={() => setViewCase(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Case Details</DialogTitle>
            <DialogDescription>Arbitration dispute resolution details.</DialogDescription>
          </DialogHeader>
          {viewCase && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Case ID</span>
                  <code className="text-right font-mono text-xs break-all">{viewCase.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Escrow ID</span>
                  <code className="text-right font-mono text-xs break-all">{viewCase.escrowId}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={(STATUS_CONFIG[viewCase.status]?.variant ?? "default") as "default" | "success" | "warning" | "accent" | "danger"}>
                    {STATUS_CONFIG[viewCase.status]?.label ?? viewCase.status}
                  </Badge>
                </div>
                {viewCase.outcome && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Outcome</span>
                    <Badge variant={(OUTCOME_CONFIG[viewCase.outcome]?.variant ?? "default") as "default" | "success" | "warning" | "accent" | "danger"}>
                      {OUTCOME_CONFIG[viewCase.outcome]?.label ?? viewCase.outcome}
                    </Badge>
                  </div>
                )}

                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Buyer</span>
                  <Address value={viewCase.buyerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Seller</span>
                  <Address value={viewCase.sellerAddr} truncate={false} />
                </div>
                {viewCase.arbiterAddr && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Arbiter</span>
                    <Address value={viewCase.arbiterAddr} truncate={false} />
                  </div>
                )}
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Disputed Amount</span>
                  <span className="tabular-nums font-medium">{formatCurrency(viewCase.disputedAmount)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Arbitration Fee</span>
                  <span className="tabular-nums">{formatCurrency(viewCase.fee)}</span>
                </div>
                {viewCase.splitPct !== undefined && viewCase.splitPct > 0 && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Split (to Buyer)</span>
                    <span className="tabular-nums">{viewCase.splitPct}%</span>
                  </div>
                )}

                <hr className="border-border" />
                <div className="flex flex-col gap-1">
                  <span className="text-xs text-muted-foreground">Reason</span>
                  <p className="text-sm">{viewCase.reason}</p>
                </div>
                {viewCase.decision && (
                  <div className="flex flex-col gap-1">
                    <span className="text-xs text-muted-foreground">Decision</span>
                    <p className="text-sm">{viewCase.decision}</p>
                  </div>
                )}

                {viewCase.contractId && (
                  <>
                    <hr className="border-border" />
                    <div className="flex items-start justify-between gap-4">
                      <span className="text-xs text-muted-foreground">Contract</span>
                      <code className="text-right font-mono text-xs">{viewCase.contractId}</code>
                    </div>
                    <div className="flex items-start justify-between gap-4">
                      <span className="text-xs text-muted-foreground">Auto-Resolvable</span>
                      <Badge variant={viewCase.autoResolvable ? "success" : "default"}>
                        {viewCase.autoResolvable ? "yes" : "no"}
                      </Badge>
                    </div>
                  </>
                )}

                {viewCase.evidence && viewCase.evidence.length > 0 && (
                  <>
                    <hr className="border-border" />
                    <span className="text-xs font-medium text-muted-foreground">
                      Evidence ({viewCase.evidence.length})
                    </span>
                    <div className="flex flex-col gap-2">
                      {viewCase.evidence.map((ev) => (
                        <div key={ev.id} className="rounded-md border bg-background px-3 py-2">
                          <div className="flex items-center gap-2 text-xs text-muted-foreground">
                            <Badge variant="default">{ev.role}</Badge>
                            <Badge variant="default">{ev.type}</Badge>
                            <span>{relativeTime(ev.submittedAt)}</span>
                          </div>
                          <p className="mt-1 text-xs break-all">
                            {ev.content.length > 200
                              ? ev.content.slice(0, 200) + "..."
                              : ev.content}
                          </p>
                        </div>
                      ))}
                    </div>
                  </>
                )}

                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Filed</span>
                  <span>{relativeTime(viewCase.filedAt)}</span>
                </div>
                {viewCase.resolvedAt && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Resolved</span>
                    <span>{relativeTime(viewCase.resolvedAt)}</span>
                  </div>
                )}
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewCase(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
