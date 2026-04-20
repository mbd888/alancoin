import { useState } from "react";
import { AlertTriangle, DollarSign, Loader2, Plus, TrendingDown } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { CHART_COLORS } from "@/lib/chart-theme";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard, Skeleton } from "@/components/ui/skeleton";
import { formatCurrency } from "@/lib/utils";
import { toast } from "sonner";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";

interface CostCenter {
  id: string;
  name: string;
  department: string;
  monthlyBudget: string;
  warnAtPercent: number;
  active: boolean;
}

interface PeriodSummary {
  costCenterName: string;
  department: string;
  totalSpend: string;
  txCount: number;
  topService: string;
  budgetUsedPct: number;
}

interface Report {
  period: string;
  totalSpend: string;
  costCenterCount: number;
  summaries: PeriodSummary[];
}


export function ChargebackPage() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [department, setDepartment] = useState("");
  const [monthlyBudget, setMonthlyBudget] = useState("");
  const [projectCode, setProjectCode] = useState("");
  const [warnAtPercent, setWarnAtPercent] = useState("80");

  const centers = useQuery({
    queryKey: ["chargeback", "cost-centers"],
    queryFn: () =>
      api.get<{ costCenters: CostCenter[]; count: number }>(
        "/chargeback/cost-centers"
      ),
  });

  const report = useQuery({
    queryKey: ["chargeback", "report"],
    queryFn: () =>
      api.get<{ report: Report }>("/chargeback/reports"),
  });

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      api.post("/chargeback/cost-centers", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chargeback", "cost-centers"] });
      queryClient.invalidateQueries({ queryKey: ["chargeback", "report"] });
      resetForm();
      toast.success("Cost center created");
    },
    onError: () => toast.error("Failed to create cost center"),
  });

  const resetForm = () => {
    setCreateOpen(false);
    setName("");
    setDepartment("");
    setMonthlyBudget("");
    setProjectCode("");
    setWarnAtPercent("80");
  };

  const handleCreate = () => {
    createMutation.mutate({
      name,
      department,
      monthlyBudget,
      ...(projectCode && { projectCode }),
      warnAtPercent: parseInt(warnAtPercent) || 80,
    });
  };

  const canSubmit = name && department && monthlyBudget;

  const summaries = report.data?.report?.summaries ?? [];
  const totalSpend = report.data?.report?.totalSpend ?? "0";

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={TrendingDown}
        title="FinOps Chargeback"
        description="Per-department agent cost attribution"
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus size={14} />
            Create Cost Center
          </Button>
        }
      />

      <div className="px-4 md:px-8 py-6">
        {/* KPI row */}
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          {report.isLoading ? (
            Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)
          ) : (
            <>
              <KpiCard
                label="Total Spend This Month"
                value={formatCurrency(totalSpend)}
                icon={DollarSign}
              />
              <KpiCard
                label="Cost Centers"
                value={centers.data?.count ?? 0}
              />
              <KpiCard
                label="Period"
                value={report.data?.report?.period ?? new Date().toISOString().slice(0, 7)}
              />
            </>
          )}
        </div>

        {/* Department breakdown chart */}
        {report.isError ? (
          <div className="mt-6 flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load chargeback report
            <Button variant="ghost" size="sm" onClick={() => report.refetch()}>
              Retry
            </Button>
          </div>
        ) : summaries.length > 0 && (
          <div className="mt-6 rounded-lg border bg-card p-5">
            <h2 className="text-sm font-medium text-foreground">
              Spend by Department
            </h2>
            <div className="mt-4 h-56">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={summaries} layout="vertical">
                  <XAxis
                    type="number"
                    tick={{ fontSize: 11, fill: "var(--foreground-muted)" }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: number) => `$${v}`}
                  />
                  <YAxis
                    type="category"
                    dataKey="costCenterName"
                    tick={{ fontSize: 12, fill: "var(--foreground-secondary)" }}
                    axisLine={false}
                    tickLine={false}
                    width={120}
                  />
                  <Tooltip
                    contentStyle={{
                      background: "var(--background-elevated)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--radius-md)",
                      fontSize: 12,
                    }}
                    formatter={(v) => [formatCurrency(String(v)), "Spend"]}
                  />
                  <Bar dataKey="totalSpend" radius={[0, 3, 3, 0]} maxBarSize={24}>
                    {summaries.map((_, i) => (
                      <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}

        {/* Cost center list */}
        <div className="mt-6">
          <h2 className="text-sm font-medium text-foreground">
            Cost Centers
          </h2>
          <div className="mt-3 flex flex-col gap-2">
            {centers.isLoading ? (
              Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-16" />
              ))
            ) : centers.data?.costCenters?.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                No cost centers configured. Create one to start tracking agent spend.
              </p>
            ) : (
              centers.data?.costCenters?.map((cc) => {
                const summary = summaries.find(
                  (s) => s.costCenterName === cc.name
                );
                const usedPct = summary?.budgetUsedPct ?? 0;
                return (
                  <div
                    key={cc.id}
                    className="flex items-center justify-between rounded-lg border bg-card px-5 py-4"
                  >
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-foreground">
                          {cc.name}
                        </span>
                        <Badge variant="default">{cc.department}</Badge>
                        {!cc.active && <Badge variant="danger">inactive</Badge>}
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">
                        Budget: {formatCurrency(cc.monthlyBudget)}/month
                        {summary && (
                          <span className="ml-3">
                            Spent: {formatCurrency(summary.totalSpend)} ({summary.txCount} txns)
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-3">
                      <div className="w-24">
                        <div className="flex items-center justify-between text-[10px] tabular-nums text-muted-foreground">
                          <span>{usedPct.toFixed(0)}%</span>
                        </div>
                        <div className="mt-0.5 h-1.5 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full"
                            style={{
                              width: `${Math.min(usedPct, 100)}%`,
                              backgroundColor:
                                usedPct >= 100
                                  ? "var(--color-danger)"
                                  : usedPct >= cc.warnAtPercent
                                    ? "var(--color-warning)"
                                    : "var(--color-accent-6)",
                            }}
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                );
              })
            )}
          </div>
        </div>
      </div>

      {/* Create Cost Center Dialog */}
      <Dialog open={createOpen} onOpenChange={() => resetForm()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Cost Center</DialogTitle>
            <DialogDescription>
              Define a budget envelope for tracking agent spend by department.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="flex flex-col gap-4">
              <Input
                id="cc-name"
                label="Name"
                placeholder="e.g. Engineering AI Tools"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
              />
              <Input
                id="cc-dept"
                label="Department"
                placeholder="e.g. Engineering"
                value={department}
                onChange={(e) => setDepartment(e.target.value)}
              />
              <Input
                id="cc-budget"
                label="Monthly budget (USDC)"
                placeholder="e.g. 5000"
                value={monthlyBudget}
                onChange={(e) => setMonthlyBudget(e.target.value)}
              />
              <Input
                id="cc-project"
                label="Project code (optional)"
                placeholder="e.g. PROJ-2026"
                value={projectCode}
                onChange={(e) => setProjectCode(e.target.value)}
              />
              <Input
                id="cc-warn"
                label="Alert threshold (%)"
                placeholder="80"
                value={warnAtPercent}
                onChange={(e) => setWarnAtPercent(e.target.value)}
              />
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={resetForm}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!canSubmit || createMutation.isPending}
              onClick={handleCreate}
            >
              {createMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Creating...
                </>
              ) : (
                "Create"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
