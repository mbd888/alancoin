import { useQuery } from "@tanstack/react-query";
import { api, getTenantId } from "@/lib/api-client";
import type { StreamsResponse } from "@/lib/types";

export function useStreams(limit = 50) {
  const tenantId = getTenantId();
  return useQuery({
    queryKey: ["dashboard", "streams", tenantId, limit],
    queryFn: () =>
      api.get<StreamsResponse>(
        `/tenants/${tenantId}/dashboard/streams`,
        { limit: String(limit) }
      ),
    refetchInterval: 15000,
  });
}
