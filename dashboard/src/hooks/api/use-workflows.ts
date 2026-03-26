import { useQuery } from "@tanstack/react-query";
import { api, getTenantId } from "@/lib/api-client";
import type { WorkflowsResponse } from "@/lib/types";

export function useWorkflows(limit = 50) {
  const tenantId = getTenantId();
  return useQuery({
    queryKey: ["dashboard", "workflows", tenantId, limit],
    queryFn: () =>
      api.get<WorkflowsResponse>(
        `/tenants/${tenantId}/dashboard/workflows`,
        { limit: String(limit) }
      ),
    refetchInterval: 15000,
  });
}
