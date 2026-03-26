import { useQuery } from "@tanstack/react-query";
import { api, getTenantId } from "@/lib/api-client";
import type { OffersResponse } from "@/lib/types";

export function useOffers(serviceType = "", limit = 50) {
  const tenantId = getTenantId();
  return useQuery({
    queryKey: ["dashboard", "offers", tenantId, serviceType, limit],
    queryFn: () => {
      const params: Record<string, string> = { limit: String(limit) };
      if (serviceType) params.serviceType = serviceType;
      return api.get<OffersResponse>(
        `/tenants/${tenantId}/dashboard/offers`,
        params
      );
    },
    refetchInterval: 30000,
  });
}
