import { useQuery } from "@tanstack/react-query";
import { api, getTenantId } from "@/lib/api-client";
import type { EscrowsResponse } from "@/lib/types";

export function useEscrows(limit = 50) {
  const tenantId = getTenantId();
  return useQuery({
    queryKey: ["dashboard", "escrows", tenantId, limit],
    queryFn: () =>
      api.get<EscrowsResponse>(
        `/tenants/${tenantId}/dashboard/escrows`,
        { limit: String(limit) }
      ),
    refetchInterval: 15000,
  });
}
