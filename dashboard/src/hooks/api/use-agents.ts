import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import type { AgentsResponse } from "@/lib/types";

export function useAgents() {
  return useQuery({
    queryKey: ["agents"],
    queryFn: () => api.get<AgentsResponse>("/agents"),
  });
}
