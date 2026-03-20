import { useState } from "react";
import {
  Activity,
  DollarSign,
  Radio,
  Bot,
  TrendingUp,
  AlertTriangle,
  Percent,
} from "lucide-react";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard, Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { useOverview, useUsage, useTopServices, useDenials } from "@/hooks/api/use-dashboard";
import { formatCurrency, formatCompact, relativeTime } from "@/lib/utils";
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
import { Link } from "@tanstack/react-router";

const INTERVAL_TABS = [
  { id: "hour", label: "Hourly" },
  { id: "day", label: "Daily" },
  { id: "week", label: "Weekly" },
];

const BAR_COLORS = [
  "oklch(0.55 0.16 250)", // accent
  "oklch(0.60 0.14 250)",
  "oklch(0.65 0.12 250)",
  "oklch(0.50 0.10 250)",
  "oklch(0.45 0.08 250)",
];

// Precomputed heights so we don't call Math.random() in render
const CHART_SKELETON_HEIGHTS = [
  "35%", "52%", "44%", "68%", "57%", "73%", "61%", "49%",
  "78%", "55%", "42%", "66%", "71%", "47%", "59%", "64%",
  "38%", "75%", "53%", "45%",
];

export function OverviewPage() {
  const [interval, setInterval] = useState<"hour" | "day" | "week">("day");
  const overview = useOverview();
  const usage = useUsage(interval);
  const topServices = useTopServices(5);
  const denials = useDenials(5);

  const o = overview.data;

  const intervalLabel =
    interval === "hour" ? "24 hours" : interval === "day" ? "30 days" : "12 weeks";

  return (
    <div className="min-h-screen">
      {/* Page header */}
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Overview</h1>
        {o ? (
          <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
            {o.tenant.name} &middot; <Badge variant="accent">{o.tenant.plan}</Badge>
          </p>
        ) : (
          <Skeleton className="mt-1.5 h-3.5 w-40" />
        )}
      </header>

      <div className="px-8 py-6">
        {/* KPI cards — 5-up */}
        <div className="grid grid-cols-5 gap-3">
          {overview.isLoading ? (
            Array.from({ length: 5 }).map((_, i) => <SkeletonCard key={i} />)
          ) : overview.isError ? (
            <div className="col-span-5 flex items-center justify-center gap-2 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] py-8 text-[13px] text-[var(--color-danger)]">
              <AlertTriangle size={14} />
              Failed to load overview
            </div>
          ) : (
            <>
              <KpiCard
                label="Total Requests"
                value={formatCompact(o?.billing.totalRequests ?? 0)}
                icon={Activity}
              />
              <KpiCard
                label="Settled Volume"
                value={formatCurrency(o?.billing.settledVolume ?? "0")}
                icon={DollarSign}
              />
              <KpiCard
                label="Fees Collected"
                value={formatCurrency(o?.billing.feesCollected ?? "0")}
                change={`${((o?.billing.takeRateBps ?? 0) / 100).toFixed(1)}% take rate`}
                changeType="neutral"
                icon={Percent}
              />
              <KpiCard
                label="Active Sessions"
                value={o?.activeSessions ?? 0}
                icon={Radio}
              />
              <KpiCard
                label="Registered Agents"
                value={o?.agentCount ?? 0}
                icon={Bot}
              />
            </>
          )}
        </div>

        {/* Usage chart */}
        <div className="mt-6 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-[14px] font-medium text-[var(--foreground)]">
                Request Volume
              </h2>
              <p className="text-[12px] text-[var(--foreground-muted)]">
                Last {intervalLabel}
              </p>
            </div>
            <div className="flex items-center gap-3">
              <Tabs
                tabs={INTERVAL_TABS}
                active={interval}
                onChange={(id) => setInterval(id as "hour" | "day" | "week")}
              />
              <TrendingUp size={16} strokeWidth={1.5} className="text-[var(--foreground-disabled)]" />
            </div>
          </div>
          <div className="h-64">
            {usage.isLoading ? (
              <div className="flex h-full flex-col justify-end gap-1">
                {/* Chart skeleton: bars of varying height */}
                <div className="flex h-full items-end gap-1 px-12">
                  {CHART_SKELETON_HEIGHTS.map((h, i) => (
                    <div
                      key={i}
                      className="flex-1 animate-pulse rounded-t bg-[var(--color-gray-3)]"
                      style={{ height: h }}
                    />
                  ))}
                </div>
                <Skeleton className="mx-12 h-3" />
              </div>
            ) : usage.isError ? (
              <div className="flex h-full items-center justify-center text-[13px] text-[var(--color-danger)]">
                <AlertTriangle size={14} className="mr-2" />
                Failed to load usage data
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={usage.data?.points ?? []}>
                  <defs>
                    <linearGradient id="fillRequests" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="oklch(0.55 0.16 250)" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="oklch(0.55 0.16 250)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="var(--color-gray-3)"
                    vertical={false}
                  />
                  <XAxis
                    dataKey="period"
                    tick={{ fontSize: 11, fill: "var(--foreground-muted)" }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: string) => {
                      const d = new Date(v);
                      return interval === "hour"
                        ? `${d.getHours()}:00`
                        : `${d.getMonth() + 1}/${d.getDate()}`;
                    }}
                  />
                  <YAxis
                    tick={{ fontSize: 11, fill: "var(--foreground-muted)" }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: number) => formatCompact(v)}
                    width={48}
                  />
                  <Tooltip
                    contentStyle={{
                      background: "var(--background-elevated)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--radius-md)",
                      fontSize: 12,
                      color: "var(--foreground)",
                    }}
                    labelFormatter={(v) =>
                      new Date(String(v)).toLocaleDateString("en-US", {
                        month: "short",
                        day: "numeric",
                        hour: interval === "hour" ? "numeric" : undefined,
                      })
                    }
                  />
                  <Area
                    type="monotone"
                    dataKey="requests"
                    stroke="oklch(0.55 0.16 250)"
                    strokeWidth={1.5}
                    fill="url(#fillRequests)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>

        {/* Bottom row */}
        <div className="mt-4 grid grid-cols-5 gap-4">
          {/* Top services — horizontal bar chart (3 cols) */}
          <div className="col-span-3 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
            <h2 className="text-[14px] font-medium text-[var(--foreground)]">
              Top Services by Volume
            </h2>
            {topServices.isLoading ? (
              <div className="mt-4 flex flex-col gap-3">
                {Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} className="h-8" />
                ))}
              </div>
            ) : topServices.data?.services && topServices.data.services.length > 0 ? (
              <div className="mt-4 h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={topServices.data.services}
                    layout="vertical"
                    margin={{ left: 0, right: 16, top: 0, bottom: 0 }}
                  >
                    <XAxis
                      type="number"
                      tick={{ fontSize: 11, fill: "var(--foreground-muted)" }}
                      axisLine={false}
                      tickLine={false}
                      tickFormatter={(v: number) => formatCompact(v)}
                    />
                    <YAxis
                      type="category"
                      dataKey="serviceType"
                      tick={{ fontSize: 12, fill: "var(--foreground-secondary)" }}
                      axisLine={false}
                      tickLine={false}
                      width={90}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "var(--background-elevated)",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--radius-md)",
                        fontSize: 12,
                        color: "var(--foreground)",
                      }}
                      formatter={(v) => [formatCompact(Number(v)), "Requests"]}
                    />
                    <Bar dataKey="requests" radius={[0, 3, 3, 0]} maxBarSize={24}>
                      {topServices.data.services.map((_, i) => (
                        <Cell key={i} fill={BAR_COLORS[i % BAR_COLORS.length]} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
            ) : (
              <p className="mt-4 text-[12px] text-[var(--foreground-muted)]">No data yet.</p>
            )}
          </div>

          {/* Recent denials (2 cols) */}
          <div className="col-span-2 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <AlertTriangle size={14} strokeWidth={1.8} className="text-[var(--color-warning)]" />
                <h2 className="text-[14px] font-medium text-[var(--foreground)]">
                  Recent Denials
                </h2>
              </div>
              <Link
                to="/sessions"
                className="text-[11px] text-[var(--color-accent-7)] hover:underline"
              >
                View all
              </Link>
            </div>
            <div className="mt-4 flex flex-col gap-2.5">
              {denials.isLoading ? (
                Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} className="h-12" />
                ))
              ) : denials.data?.denials.length === 0 ? (
                <p className="py-4 text-center text-[12px] text-[var(--foreground-muted)]">
                  No policy denials. All clear.
                </p>
              ) : (
                denials.data?.denials.map((d) => (
                  <div
                    key={d.id}
                    className="flex items-start justify-between gap-3 rounded-[var(--radius-md)] border border-[var(--border-subtle)] bg-[var(--background)] px-3 py-2.5"
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <Badge variant="danger">{d.policyName}</Badge>
                        <span className="text-[11px] text-[var(--foreground-muted)]">
                          {d.serviceType}
                        </span>
                      </div>
                      <p className="mt-0.5 truncate text-[12px] text-[var(--foreground-muted)]">
                        {d.reason}
                      </p>
                    </div>
                    <span className="shrink-0 text-[11px] tabular-nums text-[var(--foreground-disabled)]">
                      {relativeTime(d.timestamp)}
                    </span>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
