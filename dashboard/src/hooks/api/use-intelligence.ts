import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";

export interface IntelligenceProfile {
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

export interface Benchmarks {
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

export function useLeaderboard(limit = 100) {
  return useQuery({
    queryKey: ["intelligence", "leaderboard", limit],
    queryFn: () =>
      api.get<{ agents: IntelligenceProfile[]; count: number }>(
        "/intelligence/network/leaderboard",
        { limit: String(limit) }
      ),
  });
}

export function useBenchmarks() {
  return useQuery({
    queryKey: ["intelligence", "benchmarks"],
    queryFn: () => api.get<Benchmarks>("/intelligence/network/benchmarks"),
  });
}
