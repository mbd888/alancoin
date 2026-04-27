import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";

export interface ForensicsAlert {
  id: string;
  agentAddr: string;
  type: string;
  severity: "info" | "warning" | "critical";
  message: string;
  score: number;
  baseline: number;
  actual: number;
  sigma: number;
  detectedAt: string;
  acknowledged: boolean;
}

export type AlertsResponse = {
  alerts: ForensicsAlert[];
  count: number;
};

export function useAlerts(limit = 100) {
  return useQuery({
    queryKey: ["forensics", "alerts", limit],
    queryFn: () =>
      api.get<AlertsResponse>("/forensics/alerts", {
        limit: String(limit),
      }),
  });
}

export function acknowledgeAlert(alertId: string) {
  return api.post(`/forensics/alerts/${alertId}/acknowledge`);
}
