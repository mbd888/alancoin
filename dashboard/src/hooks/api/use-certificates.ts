import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { toast } from "sonner";

export interface KYACertificate {
  id: string;
  agentAddr: string;
  did: string;
  org: {
    tenantId: string;
    orgName: string;
    department: string;
    authorizedBy: string;
  };
  permissions: {
    maxSpendPerDay?: string;
    allowedApis?: string[];
  };
  reputation: {
    trustTier: string;
    traceRankScore: number;
    successRate: number;
    disputeRate: number;
    txCount: number;
    accountAgeDays: number;
  };
  status: "active" | "revoked" | "expired";
  issuedAt: string;
  expiresAt: string;
  revokedAt?: string;
}

export type CertificatesResponse = {
  certificates: KYACertificate[];
  count: number;
};

export function useCertificates(limit = 100) {
  return useQuery({
    queryKey: ["kya", "certificates", limit],
    queryFn: () =>
      api.get<CertificatesResponse>("/kya/tenants/default/certificates", {
        limit: String(limit),
      }),
  });
}

export function useComplianceReport(certId: string | null) {
  return useQuery({
    queryKey: ["kya", "compliance", certId],
    queryFn: () =>
      api.get<{ report: Record<string, unknown> }>(
        `/kya/certificates/${certId}/compliance`
      ),
    enabled: !!certId,
  });
}

export function useIssueCertificate(onSuccess?: () => void) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { agentAddr: string; orgName: string; department: string }) =>
      api.post("/kya/certificates", {
        agentAddr: data.agentAddr,
        org: {
          tenantId: "default",
          orgName: data.orgName,
          department: data.department,
          authorizedBy: "dashboard",
          authMethod: "api_key",
        },
        permissions: {},
        validDays: 365,
      }),
    onSuccess: () => {
      toast.success("KYA certificate issued");
      queryClient.invalidateQueries({ queryKey: ["kya"] });
      onSuccess?.();
    },
    onError: () => toast.error("Failed to issue certificate"),
  });
}

export function useRevokeCertificate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (certId: string) =>
      api.post(`/kya/certificates/${certId}/revoke`, { reason: "Revoked via dashboard" }),
    onSuccess: () => {
      toast.success("Certificate revoked");
      queryClient.invalidateQueries({ queryKey: ["kya"] });
    },
  });
}
