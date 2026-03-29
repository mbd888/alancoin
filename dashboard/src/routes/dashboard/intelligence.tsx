import { useState } from "react";
import {
  Brain,
  TrendingUp,
  Shield,
  Award,
  Users,
  BarChart3,
  ArrowUpRight,
  ArrowDownRight,
  Minus,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/layouts/page-header";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { KpiCard } from "@/components/ui/kpi-card";

interface IntelligenceProfile {
  address: string;
  creditScore: number;
  riskScore: number;
  compositeScore: number;
  tier: string;
  computeRunId: string;
  computedAt: string;
  credit: {
    traceRankInput: number;
    reputationInput: number;
    disputeRate: number;
    txSuccessRate: number;
    totalVolume: number;
  };
  risk: {
    anomalyCount30d: number;
    criticalAlerts: number;
    meanAmount: number;
    stdDevAmount: number;
    forensicScore: number;
    behavioralVolatility: number;
  };
  network: {
    inDegree: number;
    outDegree: number;
    clusteringCoefficient: number;
    bridgeScore: number;
  };
  operational: {
    totalTxns: number;
    daysOnNetwork: number;
  };
  trends: {
    creditDelta7d: number;
    creditDelta30d: number;
    riskDelta7d: number;
    riskDelta30d: number;
  };
}

interface Benchmarks {
  totalAgents: number;
  avgCreditScore: number;
  medianCreditScore: number;
  avgRiskScore: number;
  p90CreditScore: number;
  p10CreditScore: number;
  avgCompositeScore: number;
  computeRunId: string;
  computedAt: string;
}

const TIER_COLORS: Record<string, string> = {
  diamond: "text-[#b9f2ff]",
  platinum: "text-[#e5e4e2]",
  gold: "text-[#ffd700]",
  silver: "text-[#c0c0c0]",
  bronze: "text-[#cd7f32]",
  unknown: "text-muted-foreground",
};

const TIER_BADGE: Record<string, "default" | "success" | "warning" | "danger"> = {
  diamond: "success",
  platinum: "success",
  gold: "warning",
  silver: "default",
  bronze: "default",
  unknown: "default",
};

const VIEW_TABS = [
  { id: "leaderboard", label: "Leaderboard" },
  { id: "benchmarks", label: "Network Benchmarks" },
];

function DeltaIndicator({ value }: { value: number }) {
  if (value > 0) {
    return (
      <span className="inline-flex items-center gap-0.5 text-xs font-medium text-success">
        <ArrowUpRight size={11} />+{value.toFixed(1)}
      </span>
    );
  }
  if (value < 0) {
    return (
      <span className="inline-flex items-center gap-0.5 text-xs font-medium text-destructive">
        <ArrowDownRight size={11} />{value.toFixed(1)}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-0.5 text-xs text-muted-foreground">
      <Minus size={11} />0.0
    </span>
  );
}

function ScoreBar({ score, maxScore = 100 }: { score: number; maxScore?: number }) {
  const pct = Math.min(100, (score / maxScore) * 100);
  const color =
    score >= 75 ? "var(--color-success)" :
    score >= 50 ? "var(--color-warning)" :
    score >= 25 ? "var(--color-warning)" :
    "var(--color-danger)";

  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-16 rounded-full bg-background">
        <div
          className="h-full rounded-full transition-all"
          style={{ width: `${pct}%`, backgroundColor: color }}
        />
      </div>
      <span className="text-xs font-medium tabular-nums">{score.toFixed(1)}</span>
    </div>
  );
}

export function IntelligencePage() {
  const [view, setView] = useState("leaderboard");

  const leaderboard = useQuery({
    queryKey: ["intelligence", "leaderboard"],
    queryFn: () =>
      api.get<{ agents: IntelligenceProfile[]; count: number }>(
        "/intelligence/network/leaderboard",
        { limit: "100" }
      ),
  });

  const benchmarks = useQuery({
    queryKey: ["intelligence", "benchmarks"],
    queryFn: () => api.get<Benchmarks>("/intelligence/network/benchmarks"),
  });

  const agents = leaderboard.data?.agents ?? [];
  const bench = benchmarks.data;

  const diamondCount = agents.filter((a) => a.tier === "diamond").length;
  const platinumCount = agents.filter((a) => a.tier === "platinum").length;
  const avgCredit = bench?.avgCreditScore ?? 0;
  const avgRisk = bench?.avgRiskScore ?? 0;

  return (
    <div className="min-h-screen">
      <PageHeader icon={Brain} title="Agent Intelligence" description="Unified credit scores, risk profiles, and network intelligence for all agents" />

      {/* KPI Cards */}
      <div className="px-4 md:px-8 py-6">
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
          <KpiCard
            label="Avg Credit Score"
            value={avgCredit.toFixed(1)}
            icon={TrendingUp}
            change={bench ? `median ${bench.medianCreditScore.toFixed(1)}` : undefined}
            changeType="neutral"
          />
          <KpiCard
            label="Avg Risk Score"
            value={avgRisk.toFixed(1)}
            icon={Shield}
            change={avgRisk < 30 ? "Low risk" : avgRisk < 60 ? "Moderate" : "Elevated"}
            changeType={avgRisk < 30 ? "positive" : avgRisk < 60 ? "neutral" : "negative"}
          />
          <KpiCard
            label="Diamond / Platinum"
            value={`${diamondCount} / ${platinumCount}`}
            icon={Award}
            change={`of ${agents.length} total agents`}
            changeType="neutral"
          />
          <KpiCard
            label="Credit P90 / P10"
            value={bench ? `${bench.p90CreditScore.toFixed(0)} / ${bench.p10CreditScore.toFixed(0)}` : "--"}
            icon={BarChart3}
            change="90th vs 10th percentile"
            changeType="neutral"
          />
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={VIEW_TABS} active={view} onChange={setView} />
      </div>

      {/* Content */}
      <div className="px-4 md:px-8 py-4">
        {view === "leaderboard" ? (
          leaderboard.isLoading ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-16" />
              ))}
            </div>
          ) : agents.length === 0 ? (
            <EmptyState
              icon={Users}
              title="No intelligence data yet"
              description="Intelligence profiles are computed automatically once agents start transacting."
            />
          ) : (
            <div className="overflow-hidden rounded-lg border">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-card">
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">#</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Agent</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Tier</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Credit</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Risk</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Composite</th>
                    <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">7d Trend</th>
                    <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">Txns</th>
                  </tr>
                </thead>
                <tbody>
                  {agents.map((agent, i) => (
                    <tr
                      key={agent.address}
                      className="border-b last:border-0 transition-colors hover:bg-accent"
                    >
                      <td className="px-4 py-3 text-muted-foreground tabular-nums">
                        {i + 1}
                      </td>
                      <td className="px-4 py-3">
                        <span className="font-mono text-xs">
                          {agent.address.slice(0, 10)}...{agent.address.slice(-4)}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={TIER_BADGE[agent.tier] ?? "default"}>
                          {agent.tier}
                        </Badge>
                      </td>
                      <td className="px-4 py-3">
                        <ScoreBar score={agent.creditScore} />
                      </td>
                      <td className="px-4 py-3">
                        <ScoreBar score={agent.riskScore} />
                      </td>
                      <td className="px-4 py-3 tabular-nums font-medium">
                        {agent.compositeScore.toFixed(1)}
                      </td>
                      <td className="px-4 py-3">
                        <DeltaIndicator value={agent.trends.creditDelta7d} />
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums text-muted-foreground">
                        {agent.operational.totalTxns.toLocaleString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        ) : (
          /* Benchmarks View */
          bench ? (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div className="rounded-lg border bg-card p-6">
                <h3 className="text-xs font-medium text-muted-foreground">
                  Credit Score Distribution
                </h3>
                <div className="mt-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">P90 (Top 10%)</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.p90CreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">Median</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.medianCreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">Average</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.avgCreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">P10 (Bottom 10%)</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.p10CreditScore.toFixed(1)}</span>
                  </div>
                </div>
              </div>

              <div className="rounded-lg border bg-card p-6">
                <h3 className="text-xs font-medium text-muted-foreground">
                  Network Health
                </h3>
                <div className="mt-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">Total Agents</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.totalAgents}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">Avg Composite</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.avgCompositeScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">Avg Risk</span>
                    <span className="text-sm font-semibold tabular-nums">{bench.avgRiskScore.toFixed(1)}</span>
                  </div>
                </div>
              </div>

              <div className="rounded-lg border bg-card p-6">
                <h3 className="text-xs font-medium text-muted-foreground">
                  Tier Distribution
                </h3>
                <div className="mt-4 space-y-3">
                  {["diamond", "platinum", "gold", "silver", "bronze"].map((tier) => {
                    const count = agents.filter((a) => a.tier === tier).length;
                    const pct = agents.length > 0 ? ((count / agents.length) * 100).toFixed(0) : "0";
                    return (
                      <div key={tier} className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <span className={`text-xs capitalize ${TIER_COLORS[tier]}`}>{tier}</span>
                        </div>
                        <span className="text-xs tabular-nums text-muted-foreground">
                          {count} ({pct}%)
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-48" />
              ))}
            </div>
          )
        )}
      </div>
    </div>
  );
}
