import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { BalanceResponse, LedgerHistoryResponse, CreditInfo } from "@/lib/types";

export function useBalance() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useQuery({
    queryKey: ["balance", agentAddress],
    queryFn: () => api.get<BalanceResponse>(`/agents/${agentAddress}/balance`),
    enabled: !!agentAddress,
    refetchInterval: 15000,
  });
}

export function useLedgerHistory(cursor?: string, limit = 50) {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useQuery({
    queryKey: ["ledger", agentAddress, cursor, limit],
    queryFn: () =>
      api.get<LedgerHistoryResponse>(`/agents/${agentAddress}/ledger`, {
        limit: String(limit),
        ...(cursor ? { cursor } : {}),
      }),
    enabled: !!agentAddress,
  });
}

export function useCreditInfo() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useQuery({
    queryKey: ["credit", agentAddress],
    queryFn: () => api.get<CreditInfo>(`/agents/${agentAddress}/credit`),
    enabled: !!agentAddress,
  });
}

export function useWithdraw() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useMutation({
    mutationFn: (amount: string) =>
      api.post<{ status: string; message: string; amount: string }>(
        `/agents/${agentAddress}/withdraw`,
        { amount }
      ),
  });
}
