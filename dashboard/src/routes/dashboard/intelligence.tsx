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
  AlertTriangle,
  Eye,
  MoreHorizontal,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/layouts/page-header";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { KpiCard } from "@/components/ui/kpi-card";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Address } from "@/components/ui/address";
import { useLeaderboard, useBenchmarks } from "@/hooks/api/use-intelligence";
import type { IntelligenceProfile } from "@/hooks/api/use-intelligence";

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
  const [viewAgent, setViewAgent] = useState<IntelligenceProfile | null>(null);

  const leaderboard = useLeaderboard();
  const benchmarks = useBenchmarks();

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
          ) : leaderboard.isError ? (
            <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
              <AlertTriangle size={14} />
              Failed to load leaderboard
              <Button variant="ghost" size="sm" onClick={() => leaderboard.refetch()}>
                Retry
              </Button>
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
                    <th className="w-10 px-2 py-2.5" />
                  </tr>
                </thead>
                <tbody>
                  {agents.map((agent, i) => (
                    <tr
                      key={agent.address}
                      className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
                      onClick={() => setViewAgent(agent)}
                    >
                      <td className="px-4 py-3 text-muted-foreground tabular-nums">
                        {i + 1}
                      </td>
                      <td className="px-4 py-3">
                        <Address value={agent.address} />
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
                      <td className="px-2 py-3" onClick={(e) => e.stopPropagation()}>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <button aria-label="Agent actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                              <MoreHorizontal size={15} />
                            </button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => setViewAgent(agent)}>
                              <Eye size={13} />
                              View profile
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        ) : benchmarks.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load benchmarks
            <Button variant="ghost" size="sm" onClick={() => benchmarks.refetch()}>
              Retry
            </Button>
          </div>
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

      <Dialog open={!!viewAgent} onOpenChange={() => setViewAgent(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Agent Intelligence Profile</DialogTitle>
            <DialogDescription>Full credit, risk, and network analysis.</DialogDescription>
          </DialogHeader>
          {viewAgent && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Address</span>
                  <Address value={viewAgent.address} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Tier</span>
                  <Badge variant={TIER_BADGE[viewAgent.tier] ?? "default"}>
                    {viewAgent.tier}
                  </Badge>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Scores</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Credit Score</span>
                  <ScoreBar score={viewAgent.creditScore} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Risk Score</span>
                  <ScoreBar score={viewAgent.riskScore} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Composite</span>
                  <span className="tabular-nums font-medium">{viewAgent.compositeScore.toFixed(1)}</span>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Credit Breakdown</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">TraceRank Input</span>
                  <span className="tabular-nums">{viewAgent.credit.traceRankInput.toFixed(2)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Reputation Input</span>
                  <span className="tabular-nums">{viewAgent.credit.reputationInput.toFixed(2)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Success Rate</span>
                  <span className="tabular-nums">{(viewAgent.credit.txSuccessRate * 100).toFixed(1)}%</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Dispute Rate</span>
                  <span className="tabular-nums">{(viewAgent.credit.disputeRate * 100).toFixed(1)}%</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Total Volume</span>
                  <span className="tabular-nums">${viewAgent.credit.totalVolume.toLocaleString()}</span>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Risk Breakdown</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Anomalies (30d)</span>
                  <span className="tabular-nums">{viewAgent.risk.anomalyCount30d}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Critical Alerts</span>
                  <span className="tabular-nums" style={{ color: viewAgent.risk.criticalAlerts > 0 ? "var(--color-danger)" : undefined }}>
                    {viewAgent.risk.criticalAlerts}
                  </span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Forensic Score</span>
                  <span className="tabular-nums">{viewAgent.risk.forensicScore.toFixed(1)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Behavioral Volatility</span>
                  <span className="tabular-nums">{viewAgent.risk.behavioralVolatility.toFixed(2)}</span>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Network</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">In-Degree / Out-Degree</span>
                  <span className="tabular-nums">{viewAgent.network.inDegree} / {viewAgent.network.outDegree}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Clustering Coefficient</span>
                  <span className="tabular-nums">{viewAgent.network.clusteringCoefficient.toFixed(3)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Bridge Score</span>
                  <span className="tabular-nums">{viewAgent.network.bridgeScore.toFixed(2)}</span>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Trends</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Credit (7d / 30d)</span>
                  <div className="flex items-center gap-3">
                    <DeltaIndicator value={viewAgent.trends.creditDelta7d} />
                    <DeltaIndicator value={viewAgent.trends.creditDelta30d} />
                  </div>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Risk (7d / 30d)</span>
                  <div className="flex items-center gap-3">
                    <DeltaIndicator value={viewAgent.trends.riskDelta7d} />
                    <DeltaIndicator value={viewAgent.trends.riskDelta30d} />
                  </div>
                </div>

                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Total Transactions</span>
                  <span className="tabular-nums">{viewAgent.operational.totalTxns.toLocaleString()}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Days on Network</span>
                  <span className="tabular-nums">{viewAgent.operational.daysOnNetwork}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewAgent(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
