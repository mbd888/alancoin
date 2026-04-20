import { useState, useEffect } from "react";
import {
  Activity,
  DollarSign,
  LayoutDashboard,
  Radio,
  Bot,
  TrendingUp,
  AlertTriangle,
  Percent,
  Brain,
  Shield,
  Rss,
} from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard, Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { useOverview, useUsage, useTopServices, useDenials } from "@/hooks/api/use-dashboard";
import { api } from "@/lib/api-client";
import { CHART_COLORS, CHART_TOOLTIP_STYLE, AREA_STROKE_COLOR } from "@/lib/chart-theme";
import { formatCurrency, formatCompact, relativeTime } from "@/lib/utils";
import { useRealtimeStore } from "@/stores/realtime-store";
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
import { Address } from "@/components/ui/address";

const INTERVAL_TABS = [
  { id: "hour", label: "Hourly" },
  { id: "day", label: "Daily" },
  { id: "week", label: "Weekly" },
];

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
  const intelligence = useQuery({
    queryKey: ["intelligence", "benchmarks"],
    queryFn: () => api.get<{
      totalAgents: number;
      avgCreditScore: number;
      medianCreditScore: number;
      avgRiskScore: number;
      avgCompositeScore: number;
      p90CreditScore: number;
      p10CreditScore: number;
    }>("/intelligence/network/benchmarks"),
  });

  const o = overview.data;
  const intel = intelligence.data;

  const intervalLabel =
    interval === "hour" ? "24 hours" : interval === "day" ? "30 days" : "12 weeks";

  return (
    <div className="min-h-screen">
      {/* Page header */}
      <PageHeader
        icon={LayoutDashboard}
        title="Overview"
        description={o?.tenant.name}
        badge={o ? <Badge variant="accent">{o.tenant.plan}</Badge> : <Skeleton className="h-3.5 w-40" />}
      />

      <div className="px-4 md:px-8 py-6">
        {/* KPI cards — 5-up */}
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
          {overview.isLoading ? (
            Array.from({ length: 5 }).map((_, i) => <SkeletonCard key={i} />)
          ) : overview.isError ? (
            <Card className="col-span-full">
              <CardContent className="flex items-center justify-center gap-2 py-8 text-sm text-destructive">
                <AlertTriangle size={14} />
                Failed to load overview
                <Button variant="ghost" size="sm" onClick={() => overview.refetch()}>
                  Retry
                </Button>
              </CardContent>
            </Card>
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
        <Card className="mt-6">
          <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
            <div>
              <CardTitle>Request Volume</CardTitle>
              <CardDescription>Last {intervalLabel}</CardDescription>
            </div>
            <div className="flex items-center gap-3">
              <Tabs
                tabs={INTERVAL_TABS}
                active={interval}
                onChange={(id) => setInterval(id as "hour" | "day" | "week")}
              />
              <TrendingUp size={16} strokeWidth={1.5} className="text-muted-foreground" />
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-64">
              {usage.isLoading ? (
                <div className="flex h-full flex-col justify-end gap-1">
                  <div className="flex h-full items-end gap-1 px-12">
                    {CHART_SKELETON_HEIGHTS.map((h, i) => (
                      <div
                        key={i}
                        className="flex-1 animate-pulse rounded-t bg-muted"
                        style={{ height: h }}
                      />
                    ))}
                  </div>
                  <Skeleton className="mx-12 h-3" />
                </div>
              ) : usage.isError ? (
                <div className="flex h-full items-center justify-center gap-2 text-sm text-destructive">
                  <AlertTriangle size={14} />
                  Failed to load usage data
                  <Button variant="ghost" size="sm" onClick={() => usage.refetch()}>
                    Retry
                  </Button>
                </div>
              ) : (
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={usage.data?.points ?? []}>
                    <defs>
                      <linearGradient id="fillRequests" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor={AREA_STROKE_COLOR} stopOpacity={0.25} />
                        <stop offset="100%" stopColor={AREA_STROKE_COLOR} stopOpacity={0} />
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
                      contentStyle={CHART_TOOLTIP_STYLE}
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
                      stroke={AREA_STROKE_COLOR}
                      strokeWidth={1.5}
                      fill="url(#fillRequests)"
                    />
                  </AreaChart>
                </ResponsiveContainer>
              )}
            </div>
          </CardContent>
        </Card>

        {/* Intelligence summary */}
        {intel && intel.totalAgents > 0 && (
          <div className="mt-4 grid grid-cols-2 lg:grid-cols-4 gap-3">
            <KpiCard
              label="Avg Credit Score"
              value={intel.avgCreditScore.toFixed(1)}
              icon={Brain}
              change={`median ${intel.medianCreditScore.toFixed(1)}`}
              changeType="neutral"
            />
            <KpiCard
              label="Avg Risk Score"
              value={intel.avgRiskScore.toFixed(1)}
              icon={Shield}
              change={intel.avgRiskScore < 30 ? "Low risk" : intel.avgRiskScore < 60 ? "Moderate" : "Elevated"}
              changeType={intel.avgRiskScore < 30 ? "positive" : intel.avgRiskScore < 60 ? "neutral" : "negative"}
            />
            <KpiCard
              label="Avg Composite"
              value={intel.avgCompositeScore.toFixed(1)}
              icon={TrendingUp}
              change={`${intel.totalAgents} agents scored`}
              changeType="neutral"
            />
            <KpiCard
              label="Credit Spread"
              value={`${intel.p90CreditScore.toFixed(0)} — ${intel.p10CreditScore.toFixed(0)}`}
              change="P90 vs P10"
              changeType="neutral"
            />
          </div>
        )}

        {/* Bottom row */}
        <div className="mt-4 grid grid-cols-1 lg:grid-cols-5 gap-4">
          {/* Top services */}
          <Card className="col-span-full lg:col-span-3">
            <CardHeader>
              <CardTitle>Top Services by Volume</CardTitle>
            </CardHeader>
            <CardContent>
              {topServices.isLoading ? (
                <div className="flex flex-col gap-3">
                  {Array.from({ length: 4 }).map((_, i) => (
                    <Skeleton key={i} className="h-8" />
                  ))}
                </div>
              ) : topServices.data?.services && topServices.data.services.length > 0 ? (
                <div className="h-48">
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
                        contentStyle={CHART_TOOLTIP_STYLE}
                        formatter={(v) => [formatCompact(Number(v)), "Requests"]}
                      />
                      <Bar dataKey="requests" radius={[0, 3, 3, 0]} maxBarSize={24}>
                        {topServices.data.services.map((_, i) => (
                          <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
                        ))}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">No data yet.</p>
              )}
            </CardContent>
          </Card>

          {/* Recent denials */}
          <Card className="col-span-full lg:col-span-2">
            <CardHeader className="flex-row items-center justify-between space-y-0">
              <div className="flex items-center gap-2">
                <AlertTriangle size={14} strokeWidth={1.8} className="text-warning" />
                <CardTitle>Recent Denials</CardTitle>
              </div>
              <Link
                to="/sessions"
                className="text-xs text-accent-7 hover:underline"
              >
                View all
              </Link>
            </CardHeader>
            <CardContent>
              <div className="flex flex-col gap-2.5">
                {denials.isLoading ? (
                  Array.from({ length: 4 }).map((_, i) => (
                    <Skeleton key={i} className="h-12" />
                  ))
                ) : denials.data?.denials.length === 0 ? (
                  <p className="py-4 text-center text-xs text-muted-foreground">
                    No policy denials. All clear.
                  </p>
                ) : (
                  denials.data?.denials.map((d) => (
                    <div
                      key={d.id}
                      className="flex items-start justify-between gap-3 rounded-md border bg-background px-3 py-2.5"
                    >
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <Badge variant="danger">{d.policyName}</Badge>
                          <span className="text-xs text-muted-foreground">
                            {d.serviceType}
                          </span>
                        </div>
                        <p className="mt-0.5 truncate text-xs text-muted-foreground">
                          {d.reason}
                        </p>
                      </div>
                      <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                        {relativeTime(d.timestamp)}
                      </span>
                    </div>
                  ))
                )}
              </div>
            </CardContent>
          </Card>
        </div>

        {/* Live Activity widget */}
        <LiveActivityWidget />
      </div>
    </div>
  );
}

function LiveActivityWidget() {
  const { connected, events, connect, disconnect } = useRealtimeStore();

  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  const recent = events.slice(0, 5);

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-2">
          <Rss size={14} className="text-muted-foreground" />
          <h3 className="text-sm font-semibold text-foreground">Live Activity</h3>
          <span
            className="inline-block size-2 rounded-full"
            style={{ backgroundColor: connected ? "var(--color-success)" : "var(--color-danger)" }}
            title={connected ? "Connected" : "Disconnected"}
          />
        </div>
        <Link
          to="/live-feed"
          className="text-xs text-accent-7 hover:underline"
        >
          View all
        </Link>
      </div>
      <div className="divide-y">
        {recent.length === 0 ? (
          <p className="px-5 py-4 text-xs text-muted-foreground">
            {connected ? "Waiting for events..." : "Connecting..."}
          </p>
        ) : (
          recent.map((event, i) => {
            const data = event.data as Record<string, string>;
            const from = data.from ?? data.authorAddr ?? "";
            const amount = data.amount ?? "";
            return (
              <div
                key={`${event.timestamp}-${i}`}
                className="flex items-center justify-between px-5 py-2"
              >
                <div className="flex items-center gap-2 text-xs">
                  <Badge
                    variant={
                      event.type.includes("dispute") || event.type.includes("alert")
                        ? "danger"
                        : event.type.includes("created") || event.type.includes("confirmed")
                          ? "success"
                          : "default"
                    }
                  >
                    {event.type.replace(/_/g, " ")}
                  </Badge>
                  {from && <Address value={from} className="text-muted-foreground" />}
                  {amount && (
                    <span className="text-xs font-medium">${amount}</span>
                  )}
                </div>
                <span className="text-xs text-muted-foreground">
                  {relativeTime(event.timestamp)}
                </span>
              </div>
            );
          })
        )}
      </div>
    </Card>
  );
}
