import { useState } from "react";
import {
  Brain,
  TrendingUp,
  TrendingDown,
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
import { Tabs } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
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
  unknown: "text-[var(--foreground-muted)]",
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
      <span className="inline-flex items-center gap-0.5 text-[11px] font-medium text-[var(--color-success)]">
        <ArrowUpRight size={11} />+{value.toFixed(1)}
      </span>
    );
  }
  if (value < 0) {
    return (
      <span className="inline-flex items-center gap-0.5 text-[11px] font-medium text-[var(--color-danger)]">
        <ArrowDownRight size={11} />{value.toFixed(1)}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-0.5 text-[11px] text-[var(--foreground-muted)]">
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
      <div className="h-1.5 w-16 rounded-full bg-[var(--background)]">
        <div
          className="h-full rounded-full transition-all"
          style={{ width: `${pct}%`, backgroundColor: color }}
        />
      </div>
      <span className="text-[12px] font-medium tabular-nums">{score.toFixed(1)}</span>
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
      <header className="border-b border-[var(--border)] px-8 py-5">
        <div className="flex items-center gap-2">
          <Brain size={18} strokeWidth={1.8} className="text-[var(--color-accent-5)]" />
          <h1 className="text-[16px] font-semibold text-[var(--foreground)]">
            Agent Intelligence
          </h1>
        </div>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Unified credit scores, risk profiles, and network intelligence for all agents
        </p>
      </header>

      {/* KPI Cards */}
      <div className="px-8 py-6">
        <div className="grid grid-cols-4 gap-3">
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
      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={VIEW_TABS} active={view} onChange={setView} />
      </div>

      {/* Content */}
      <div className="px-8 py-4">
        {view === "leaderboard" ? (
          leaderboard.isLoading ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-16" />
              ))}
            </div>
          ) : agents.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Users size={32} strokeWidth={1.2} className="text-[var(--foreground-muted)]" />
              <h3 className="mt-3 text-[14px] font-medium text-[var(--foreground)]">
                No intelligence data yet
              </h3>
              <p className="mt-1 text-[13px] text-[var(--foreground-muted)]">
                Intelligence profiles are computed automatically once agents start transacting.
              </p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-[var(--radius-lg)] border border-[var(--border-subtle)]">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="border-b border-[var(--border-subtle)] bg-[var(--background-elevated)]">
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">#</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">Agent</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">Tier</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">Credit</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">Risk</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">Composite</th>
                    <th className="px-4 py-2.5 text-left font-medium text-[var(--foreground-muted)]">7d Trend</th>
                    <th className="px-4 py-2.5 text-right font-medium text-[var(--foreground-muted)]">Txns</th>
                  </tr>
                </thead>
                <tbody>
                  {agents.map((agent, i) => (
                    <tr
                      key={agent.address}
                      className="border-b border-[var(--border-subtle)] last:border-0 transition-colors hover:bg-[var(--background-interactive)]"
                    >
                      <td className="px-4 py-3 text-[var(--foreground-muted)] tabular-nums">
                        {i + 1}
                      </td>
                      <td className="px-4 py-3">
                        <span className="font-mono text-[12px]">
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
                      <td className="px-4 py-3 text-right tabular-nums text-[var(--foreground-muted)]">
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
            <div className="grid grid-cols-3 gap-4">
              <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-6">
                <h3 className="text-[12px] font-medium text-[var(--foreground-muted)]">
                  Credit Score Distribution
                </h3>
                <div className="mt-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">P90 (Top 10%)</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.p90CreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">Median</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.medianCreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">Average</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.avgCreditScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">P10 (Bottom 10%)</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.p10CreditScore.toFixed(1)}</span>
                  </div>
                </div>
              </div>

              <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-6">
                <h3 className="text-[12px] font-medium text-[var(--foreground-muted)]">
                  Network Health
                </h3>
                <div className="mt-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">Total Agents</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.totalAgents}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">Avg Composite</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.avgCompositeScore.toFixed(1)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-[12px] text-[var(--foreground-muted)]">Avg Risk</span>
                    <span className="text-[14px] font-semibold tabular-nums">{bench.avgRiskScore.toFixed(1)}</span>
                  </div>
                </div>
              </div>

              <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-6">
                <h3 className="text-[12px] font-medium text-[var(--foreground-muted)]">
                  Tier Distribution
                </h3>
                <div className="mt-4 space-y-3">
                  {["diamond", "platinum", "gold", "silver", "bronze"].map((tier) => {
                    const count = agents.filter((a) => a.tier === tier).length;
                    const pct = agents.length > 0 ? ((count / agents.length) * 100).toFixed(0) : "0";
                    return (
                      <div key={tier} className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <span className={`text-[12px] capitalize ${TIER_COLORS[tier]}`}>{tier}</span>
                        </div>
                        <span className="text-[12px] tabular-nums text-[var(--foreground-muted)]">
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
