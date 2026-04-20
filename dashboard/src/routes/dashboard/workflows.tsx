import { useState, useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { useWorkflows } from "@/hooks/api/use-workflows";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { Workflow } from "@/lib/types";
import { GitBranch, AlertTriangle, DollarSign, CheckCircle, MoreHorizontal, Eye } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Address } from "@/components/ui/address";

const STATUS_VARIANT: Record<string, string> = {
  open: "accent",
  completed: "success",
  aborted: "danger",
};

const STATUS_TABS = [
  { id: "all", label: "All" },
  { id: "open", label: "Open" },
  { id: "completed", label: "Completed" },
  { id: "aborted", label: "Aborted" },
];

export function WorkflowsPage() {
  const [statusFilter, setStatusFilter] = useState("all");
  const [viewWorkflow, setViewWorkflow] = useState<Workflow | null>(null);
  const workflows = useWorkflows();

  const allWorkflows = workflows.data?.workflows ?? [];

  const filteredWorkflows = useMemo(() => {
    if (statusFilter === "all") return allWorkflows;
    return allWorkflows.filter((w) => w.status === statusFilter);
  }, [allWorkflows, statusFilter]);

  const counts = useMemo(() => {
    const map: Record<string, number> = { all: allWorkflows.length };
    for (const w of allWorkflows) {
      map[w.status] = (map[w.status] ?? 0) + 1;
    }
    return map;
  }, [allWorkflows]);

  const activeCount = counts.open ?? 0;
  const totalBudget = allWorkflows.reduce((sum, w) => sum + (parseFloat(w.totalBudget) || 0), 0);
  const totalSpent = allWorkflows.reduce((sum, w) => sum + (parseFloat(w.spentAmount) || 0), 0);
  const completionRate = allWorkflows.length > 0
    ? ((counts.completed ?? 0) / allWorkflows.length) * 100
    : 0;

  const tabsWithCounts = STATUS_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  const columns: Column<Workflow>[] = [
    {
      id: "id",
      header: "Workflow",
      cell: (row) => (
        <div>
          <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
          {row.name && (
            <span className="ml-2 text-xs text-muted-foreground">{row.name}</span>
          )}
        </div>
      ),
    },
    {
      id: "buyer",
      header: "Buyer",
      cell: (row) => <Address value={row.buyerAddr} />,
    },
    {
      id: "progress",
      header: "Progress",
      sortable: true,
      sortValue: (row) => row.totalSteps > 0 ? row.completedSteps / row.totalSteps : 0,
      cell: (row) => {
        const pct = row.totalSteps > 0
          ? (row.completedSteps / row.totalSteps) * 100
          : 0;
        return (
          <div className="flex items-center gap-2">
            <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-accent-foreground transition-[width] duration-300"
                style={{ width: `${pct}%` }}
              />
            </div>
            <span className="text-xs">
              {row.completedSteps}/{row.totalSteps}
            </span>
          </div>
        );
      },
    },
    {
      id: "cost",
      header: "Cost",
      numeric: true,
      sortable: true,
      sortValue: (row) => parseFloat(row.spentAmount) || 0,
      cell: (row) => (
        <span className="text-xs">
          {formatCurrency(row.spentAmount)}
          <span className="text-muted-foreground/50"> / </span>
          {formatCurrency(row.totalBudget)}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge variant={(STATUS_VARIANT[row.status] ?? "default") as "accent" | "success" | "default" | "danger"}>
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
              <button aria-label="Workflow actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewWorkflow(row)}>
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
      <PageHeader icon={GitBranch} title="Workflows" description="Multi-agent pipeline execution and budgets" />

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
        {workflows.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={GitBranch} label="Active Workflows" value={activeCount} />
            <KpiCard icon={DollarSign} label="Total Budget" value={`$${totalBudget.toFixed(2)}`} />
            <KpiCard icon={DollarSign} label="Total Spent" value={`$${totalSpent.toFixed(2)}`} />
            <KpiCard icon={CheckCircle} label="Completion Rate" value={`${completionRate.toFixed(0)}%`} />
          </>
        )}
      </div>

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {workflows.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load workflows
            <Button variant="ghost" size="sm" onClick={() => workflows.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={filteredWorkflows}
            isLoading={workflows.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewWorkflow(row)}
            dataUpdatedAt={workflows.dataUpdatedAt}
            emptyTitle={statusFilter === "all" ? "No workflows" : `No ${statusFilter} workflows`}
            emptyDescription="No workflow pipelines found."
            totalLabel={`${filteredWorkflows.length} workflows`}
          />
        )}
      </div>

      <Dialog open={!!viewWorkflow} onOpenChange={() => setViewWorkflow(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Workflow Details</DialogTitle>
            <DialogDescription>Multi-agent pipeline execution details.</DialogDescription>
          </DialogHeader>
          {viewWorkflow && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Workflow ID</span>
                  <code className="text-right font-mono text-xs">{viewWorkflow.id}</code>
                </div>
                {viewWorkflow.name && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Name</span>
                    <span>{viewWorkflow.name}</span>
                  </div>
                )}
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Buyer</span>
                  <Address value={viewWorkflow.buyerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={(STATUS_VARIANT[viewWorkflow.status] ?? "default") as "accent" | "success" | "default" | "danger"}>
                    {viewWorkflow.status}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Progress</span>
                  <div className="flex items-center gap-2">
                    <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-accent-foreground"
                        style={{ width: `${viewWorkflow.totalSteps > 0 ? (viewWorkflow.completedSteps / viewWorkflow.totalSteps) * 100 : 0}%` }}
                      />
                    </div>
                    <span className="tabular-nums">{viewWorkflow.completedSteps}/{viewWorkflow.totalSteps}</span>
                  </div>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Budget</span>
                  <span className="tabular-nums">{formatCurrency(viewWorkflow.spentAmount)} / {formatCurrency(viewWorkflow.totalBudget)}</span>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewWorkflow.createdAt)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Updated</span>
                  <span>{relativeTime(viewWorkflow.updatedAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewWorkflow(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
