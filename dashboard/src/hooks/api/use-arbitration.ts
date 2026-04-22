import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import type { ArbitrationCase, ArbitrationCasesResponse } from "@/lib/types";

export function useArbitrationCases(limit = 50) {
  return useQuery({
    queryKey: ["arbitration", "cases", limit],
    queryFn: () =>
      api.get<ArbitrationCasesResponse>("/arbitration/cases", {
        limit: String(limit),
      }),
    refetchInterval: 15000,
  });
}

export function useArbitrationCase(id: string) {
  return useQuery({
    queryKey: ["arbitration", "case", id],
    queryFn: () => api.get<{ case: ArbitrationCase }>(`/arbitration/cases/${id}`),
    enabled: !!id,
  });
}

export function useArbitrationByEscrow(escrowId: string) {
  return useQuery({
    queryKey: ["arbitration", "escrow", escrowId],
    queryFn: () =>
      api.get<ArbitrationCasesResponse>(
        `/arbitration/escrows/${escrowId}/cases`
      ),
    enabled: !!escrowId,
  });
}
