import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { WebhooksResponse } from "@/lib/types";

export function useWebhooks() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  return useQuery({
    queryKey: ["webhooks", agentAddress],
    queryFn: () => api.get<WebhooksResponse>(`/agents/${agentAddress}/webhooks`),
    enabled: !!agentAddress,
  });
}

export function useCreateWebhook() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { url: string; events: string[] }) =>
      api.post<{
        webhook: { id: string; url: string; events: string[]; active: boolean; createdAt: string };
        secret: string;
      }>(`/agents/${agentAddress}/webhooks`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["webhooks", agentAddress] }),
  });
}

export function useDeleteWebhook() {
  const agentAddress = useAuthStore((s) => s.agentAddress);
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (webhookId: string) =>
      api.delete(`/agents/${agentAddress}/webhooks/${webhookId}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["webhooks", agentAddress] }),
  });
}
