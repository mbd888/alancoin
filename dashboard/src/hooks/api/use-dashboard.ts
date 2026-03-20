import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import type {
  DashboardOverview,
  DashboardUsage,
  TopServicesResponse,
  DenialsResponse,
  SessionsResponse,
} from "@/lib/types";

// TODO: replace with real tenant ID from auth context
const TENANT_ID = "default";

export function useOverview() {
  return useQuery({
    queryKey: ["dashboard", "overview", TENANT_ID],
    queryFn: () =>
      api.get<DashboardOverview>(
        `/tenants/${TENANT_ID}/dashboard/overview`
      ),
  });
}

export function useUsage(interval: "hour" | "day" | "week" = "day") {
  return useQuery({
    queryKey: ["dashboard", "usage", TENANT_ID, interval],
    queryFn: () =>
      api.get<DashboardUsage>(
        `/tenants/${TENANT_ID}/dashboard/usage`,
        { interval }
      ),
  });
}

export function useTopServices(limit = 10) {
  return useQuery({
    queryKey: ["dashboard", "top-services", TENANT_ID, limit],
    queryFn: () =>
      api.get<TopServicesResponse>(
        `/tenants/${TENANT_ID}/dashboard/top-services`,
        { limit: String(limit) }
      ),
  });
}

export function useDenials(limit = 10) {
  return useQuery({
    queryKey: ["dashboard", "denials", TENANT_ID, limit],
    queryFn: () =>
      api.get<DenialsResponse>(
        `/tenants/${TENANT_ID}/dashboard/denials`,
        { limit: String(limit) }
      ),
  });
}

export function useSessions(limit = 50, cursor?: string) {
  return useQuery({
    queryKey: ["dashboard", "sessions", TENANT_ID, limit, cursor],
    queryFn: () => {
      const params: Record<string, string> = { limit: String(limit) };
      if (cursor) params.cursor = cursor;
      return api.get<SessionsResponse>(
        `/tenants/${TENANT_ID}/dashboard/sessions`,
        params
      );
    },
  });
}
