import { useState } from "react";
import { Shield, Plus, CheckCircle, XCircle, FileText, ExternalLink } from "lucide-react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogHeader, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { relativeTime } from "@/lib/utils";
import { toast } from "sonner";

interface KYACertificate {
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

const TIER_VARIANT: Record<string, "success" | "accent" | "warning" | "danger" | "default"> = {
  AAA: "success",
  AA: "success",
  A: "accent",
  B: "default",
  C: "warning",
  D: "danger",
};

export function CertificatesPage() {
  const [issueOpen, setIssueOpen] = useState(false);
  const [issueAddr, setIssueAddr] = useState("");
  const [issueOrg, setIssueOrg] = useState("");
  const [issueDept, setIssueDept] = useState("");
  const [complianceId, setComplianceId] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const certs = useQuery({
    queryKey: ["kya", "certificates"],
    queryFn: () =>
      api.get<{ certificates: KYACertificate[]; count: number }>(
        "/kya/tenants/default/certificates",
        { limit: "100" }
      ),
  });

  const compliance = useQuery({
    queryKey: ["kya", "compliance", complianceId],
    queryFn: () =>
      api.get<{ report: Record<string, unknown> }>(
        `/kya/certificates/${complianceId}/compliance`
      ),
    enabled: !!complianceId,
  });

  const issueMutation = useMutation({
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
      setIssueOpen(false);
      setIssueAddr("");
      setIssueOrg("");
      setIssueDept("");
    },
    onError: () => toast.error("Failed to issue certificate"),
  });

  const revokeMutation = useMutation({
    mutationFn: (certId: string) =>
      api.post(`/kya/certificates/${certId}/revoke`, { reason: "Revoked via dashboard" }),
    onSuccess: () => {
      toast.success("Certificate revoked");
      queryClient.invalidateQueries({ queryKey: ["kya"] });
    },
  });

  return (
    <div className="min-h-screen">
      <header className="flex items-center justify-between border-b border-[var(--border)] px-8 py-5">
        <div>
          <div className="flex items-center gap-2">
            <Shield size={18} strokeWidth={1.8} className="text-[var(--color-accent-6)]" />
            <h1 className="text-[16px] font-semibold text-[var(--foreground)]">
              KYA Certificates
            </h1>
          </div>
          <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
            Know Your Agent identity verification &middot; EU AI Act Article 12 ready
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setIssueOpen(true)}>
          <Plus size={14} />
          Issue Certificate
        </Button>
      </header>

      <div className="px-8 py-6">
        {certs.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="h-24 animate-pulse rounded-[var(--radius-lg)] bg-[var(--color-gray-3)]" />
            ))}
          </div>
        ) : certs.data?.certificates?.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Shield size={32} strokeWidth={1.2} className="text-[var(--foreground-disabled)]" />
            <h3 className="mt-3 text-[14px] font-medium text-[var(--foreground)]">
              No certificates issued
            </h3>
            <p className="mt-1 text-[13px] text-[var(--foreground-muted)]">
              Issue a KYA certificate to verify agent identity and enable trust-gated escrows.
            </p>
            <Button variant="primary" size="sm" className="mt-4" onClick={() => setIssueOpen(true)}>
              Issue First Certificate
            </Button>
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {certs.data?.certificates?.map((cert) => (
              <div
                key={cert.id}
                className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      {cert.status === "active" ? (
                        <CheckCircle size={15} className="text-[var(--color-success)]" />
                      ) : (
                        <XCircle size={15} className="text-[var(--color-danger)]" />
                      )}
                      <span className="font-mono text-[13px] text-[var(--foreground)]">
                        {cert.did}
                      </span>
                      <Badge variant={cert.status === "active" ? "success" : "danger"}>
                        {cert.status}
                      </Badge>
                      <Badge variant={TIER_VARIANT[cert.reputation.trustTier] ?? "default"}>
                        Tier {cert.reputation.trustTier}
                      </Badge>
                    </div>
                    <div className="mt-2 flex items-center gap-4 text-[11px] text-[var(--foreground-muted)]">
                      <span>Org: {cert.org.orgName}</span>
                      {cert.org.department && <span>Dept: {cert.org.department}</span>}
                      <span>Issued {relativeTime(cert.issuedAt)}</span>
                      <span>Expires {relativeTime(cert.expiresAt)}</span>
                    </div>
                    <div className="mt-2 flex items-center gap-4 text-[11px] tabular-nums text-[var(--foreground-muted)]">
                      <span>Score: {cert.reputation.traceRankScore.toFixed(1)}</span>
                      <span>Success: {(cert.reputation.successRate * 100).toFixed(0)}%</span>
                      <span>Disputes: {(cert.reputation.disputeRate * 100).toFixed(1)}%</span>
                      <span>Txns: {cert.reputation.txCount}</span>
                      <span>Age: {cert.reputation.accountAgeDays}d</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setComplianceId(cert.id)}
                    >
                      <FileText size={13} />
                      Compliance
                    </Button>
                    {cert.status === "active" && (
                      <Button
                        variant="danger"
                        size="sm"
                        onClick={() => revokeMutation.mutate(cert.id)}
                      >
                        Revoke
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Issue Certificate Dialog */}
      <Dialog open={issueOpen} onClose={() => setIssueOpen(false)}>
        <DialogHeader onClose={() => setIssueOpen(false)}>
          <h2 className="text-[14px] font-semibold text-[var(--foreground)]">
            Issue KYA Certificate
          </h2>
        </DialogHeader>
        <DialogBody>
          <div className="flex flex-col gap-4">
            <Input
              id="agent-addr"
              label="Agent Address"
              placeholder="0x..."
              value={issueAddr}
              onChange={(e) => setIssueAddr(e.target.value)}
              autoFocus
            />
            <Input
              id="org-name"
              label="Organization"
              placeholder="Acme Corp"
              value={issueOrg}
              onChange={(e) => setIssueOrg(e.target.value)}
            />
            <Input
              id="dept"
              label="Department"
              placeholder="Engineering"
              value={issueDept}
              onChange={(e) => setIssueDept(e.target.value)}
            />
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={() => setIssueOpen(false)}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={!issueAddr || !issueOrg}
            onClick={() =>
              issueMutation.mutate({
                agentAddr: issueAddr,
                orgName: issueOrg,
                department: issueDept,
              })
            }
          >
            Issue Certificate
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Compliance Report Dialog */}
      <Dialog open={!!complianceId} onClose={() => setComplianceId(null)}>
        <DialogHeader onClose={() => setComplianceId(null)}>
          <h2 className="text-[14px] font-semibold text-[var(--foreground)]">
            EU AI Act Article 12 — Compliance Report
          </h2>
        </DialogHeader>
        <DialogBody>
          {compliance.isLoading ? (
            <div className="py-8 text-center text-[13px] text-[var(--foreground-muted)]">
              Generating compliance report...
            </div>
          ) : compliance.data?.report ? (
            <pre className="max-h-80 overflow-auto rounded-[var(--radius-md)] border border-[var(--border-subtle)] bg-[var(--background)] p-4 font-mono text-[11px] leading-relaxed text-[var(--foreground-secondary)]">
              {JSON.stringify(compliance.data.report, null, 2)}
            </pre>
          ) : (
            <p className="text-[13px] text-[var(--foreground-muted)]">
              No report available.
            </p>
          )}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={() => setComplianceId(null)}>
            Close
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              if (compliance.data?.report) {
                const blob = new Blob(
                  [JSON.stringify(compliance.data.report, null, 2)],
                  { type: "application/json" }
                );
                const url = URL.createObjectURL(blob);
                const a = document.createElement("a");
                a.href = url;
                a.download = `compliance-${complianceId}.json`;
                a.click();
                URL.revokeObjectURL(url);
                toast.success("Report downloaded");
              }
            }}
          >
            <ExternalLink size={13} />
            Download JSON
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}
