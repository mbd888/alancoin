import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import type { AuthKeysResponse } from "@/lib/types";

export function useApiKeys() {
  return useQuery({
    queryKey: ["auth", "keys"],
    queryFn: () => api.get<AuthKeysResponse>("/auth/keys"),
  });
}

export function useCreateApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      api.post<{ apiKey: string; keyId: string; name: string; warning: string }>(
        "/auth/keys",
        { name }
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "keys"] }),
  });
}

export function useRevokeApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) => api.delete(`/auth/keys/${keyId}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "keys"] }),
  });
}

export function useRotateApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) =>
      api.post<{ apiKey: string; keyId: string; oldKeyId: string; warning: string }>(
        `/auth/keys/${keyId}/regenerate`
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "keys"] }),
  });
}
