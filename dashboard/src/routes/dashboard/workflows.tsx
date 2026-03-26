import { useState, useMemo } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { useWorkflows } from "@/hooks/api/use-workflows";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { Workflow } from "@/lib/types";

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
          <span className="font-mono text-[12px]">{row.id.slice(0, 12)}...</span>
          {row.name && (
            <span className="ml-2 text-[12px] text-[var(--foreground-muted)]">{row.name}</span>
          )}
        </div>
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
      id: "progress",
      header: "Progress",
      cell: (row) => {
        const pct = row.totalSteps > 0
          ? (row.completedSteps / row.totalSteps) * 100
          : 0;
        return (
          <div className="flex items-center gap-2">
            <div className="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--color-gray-3)]">
              <div
                className="h-full rounded-full bg-[var(--color-accent-6)] transition-[width] duration-300"
                style={{ width: `${pct}%` }}
              />
            </div>
            <span className="text-[12px]">
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
      cell: (row) => (
        <span className="text-[12px]">
          {formatCurrency(row.spentAmount)}
          <span className="text-[var(--foreground-disabled)]"> / </span>
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
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Workflows</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Multi-agent pipeline execution and budgets
        </p>
      </header>

      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={filteredWorkflows}
          isLoading={workflows.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle={statusFilter === "all" ? "No workflows" : `No ${statusFilter} workflows`}
          emptyDescription="No workflow pipelines found."
          totalLabel={`${filteredWorkflows.length} workflows`}
        />
      </div>
    </div>
  );
}
