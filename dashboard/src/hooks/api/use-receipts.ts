import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { ReceiptsResponse, ReceiptVerification } from "@/lib/types";

export function useReceipts(limit = 50) {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useQuery({
    queryKey: ["receipts", agentAddress, limit],
    queryFn: () =>
      api.get<ReceiptsResponse>(`/agents/${agentAddress}/receipts`, {
        limit: String(limit),
      }),
    enabled: !!agentAddress,
  });
}

export function useVerifyReceipt() {
  return useMutation({
    mutationFn: (receiptId: string) =>
      api.post<{ verification: ReceiptVerification }>("/receipts/verify", {
        receiptId,
      }),
  });
}
