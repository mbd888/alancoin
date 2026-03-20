import { DollarSign, Plus, TrendingDown } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard, Skeleton } from "@/components/ui/skeleton";
import { formatCurrency } from "@/lib/utils";
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

const BAR_COLORS = [
  "oklch(0.55 0.16 250)",
  "oklch(0.60 0.14 250)",
  "oklch(0.65 0.12 250)",
  "oklch(0.50 0.10 250)",
  "oklch(0.45 0.08 250)",
];

export function ChargebackPage() {
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

  const summaries = report.data?.report?.summaries ?? [];
  const totalSpend = report.data?.report?.totalSpend ?? "0";

  return (
    <div className="min-h-screen">
      <header className="flex items-center justify-between border-b border-[var(--border)] px-8 py-5">
        <div>
          <div className="flex items-center gap-2">
            <TrendingDown size={18} strokeWidth={1.8} className="text-[var(--color-accent-6)]" />
            <h1 className="text-[16px] font-semibold text-[var(--foreground)]">
              FinOps Chargeback
            </h1>
          </div>
          <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
            Per-department agent cost attribution
          </p>
        </div>
        <Button variant="primary" size="sm">
          <Plus size={14} />
          Create Cost Center
        </Button>
      </header>

      <div className="px-8 py-6">
        {/* KPI row */}
        <div className="grid grid-cols-3 gap-4">
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
        {summaries.length > 0 && (
          <div className="mt-6 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
            <h2 className="text-[14px] font-medium text-[var(--foreground)]">
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
                      <Cell key={i} fill={BAR_COLORS[i % BAR_COLORS.length]} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}

        {/* Cost center list */}
        <div className="mt-6">
          <h2 className="text-[14px] font-medium text-[var(--foreground)]">
            Cost Centers
          </h2>
          <div className="mt-3 flex flex-col gap-2">
            {centers.isLoading ? (
              Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-16" />
              ))
            ) : centers.data?.costCenters?.length === 0 ? (
              <p className="py-8 text-center text-[13px] text-[var(--foreground-muted)]">
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
                    className="flex items-center justify-between rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] px-5 py-4"
                  >
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="text-[13px] font-medium text-[var(--foreground)]">
                          {cc.name}
                        </span>
                        <Badge variant="default">{cc.department}</Badge>
                        {!cc.active && <Badge variant="danger">inactive</Badge>}
                      </div>
                      <div className="mt-1 text-[12px] text-[var(--foreground-muted)]">
                        Budget: {formatCurrency(cc.monthlyBudget)}/month
                        {summary && (
                          <span className="ml-3">
                            Spent: {formatCurrency(summary.totalSpend)} ({summary.txCount} txns)
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-3">
                      {/* Budget usage bar */}
                      <div className="w-24">
                        <div className="flex items-center justify-between text-[10px] tabular-nums text-[var(--foreground-muted)]">
                          <span>{usedPct.toFixed(0)}%</span>
                        </div>
                        <div className="mt-0.5 h-1.5 overflow-hidden rounded-full bg-[var(--color-gray-3)]">
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
    </div>
  );
}
