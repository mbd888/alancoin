import { useState } from "react";
import { Shield, Plus, CheckCircle, XCircle, FileText, ExternalLink, Loader2 } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { relativeTime } from "@/lib/utils";
import { EmptyState } from "@/components/ui/empty-state";
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
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);
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
      <PageHeader
        icon={Shield}
        title="KYA Certificates"
        description="Know Your Agent identity verification &middot; EU AI Act Article 12 ready"
        actions={
          <Button variant="primary" size="sm" onClick={() => setIssueOpen(true)}>
            <Plus size={14} />
            Issue Certificate
          </Button>
        }
      />

      <div className="px-4 md:px-8 py-6">
        {certs.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="h-24 animate-pulse rounded-lg bg-muted" />
            ))}
          </div>
        ) : certs.data?.certificates?.length === 0 ? (
          <EmptyState
            icon={Shield}
            title="No certificates issued"
            description="Issue a KYA certificate to verify agent identity and enable trust-gated escrows."
            action={
              <Button variant="primary" size="sm" onClick={() => setIssueOpen(true)}>
                Issue First Certificate
              </Button>
            }
          />
        ) : (
          <div className="flex flex-col gap-3">
            {certs.data?.certificates?.map((cert) => (
              <div
                key={cert.id}
                className="rounded-lg border bg-card p-5"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      {cert.status === "active" ? (
                        <CheckCircle size={15} className="text-success" />
                      ) : (
                        <XCircle size={15} className="text-destructive" />
                      )}
                      <span className="font-mono text-sm text-foreground">
                        {cert.did}
                      </span>
                      <Badge variant={cert.status === "active" ? "success" : "danger"}>
                        {cert.status}
                      </Badge>
                      <Badge variant={TIER_VARIANT[cert.reputation.trustTier] ?? "default"}>
                        Tier {cert.reputation.trustTier}
                      </Badge>
                    </div>
                    <div className="mt-2 flex items-center gap-4 text-xs text-muted-foreground">
                      <span>Org: {cert.org.orgName}</span>
                      {cert.org.department && <span>Dept: {cert.org.department}</span>}
                      <span>Issued {relativeTime(cert.issuedAt)}</span>
                      <span>Expires {relativeTime(cert.expiresAt)}</span>
                    </div>
                    <div className="mt-2 flex items-center gap-4 text-xs tabular-nums text-muted-foreground">
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
                        onClick={() => setConfirmRevoke(cert.id)}
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
      <Dialog open={issueOpen} onOpenChange={setIssueOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Issue KYA Certificate</DialogTitle>
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
              disabled={!issueAddr || !issueOrg || issueMutation.isPending}
              onClick={() =>
                issueMutation.mutate({
                  agentAddr: issueAddr,
                  orgName: issueOrg,
                  department: issueDept,
                })
              }
            >
              {issueMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Issuing...
                </>
              ) : (
                "Issue Certificate"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Revoke Confirmation Dialog */}
      <Dialog open={!!confirmRevoke} onOpenChange={() => setConfirmRevoke(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke Certificate</DialogTitle>
            <DialogDescription>
              This certificate will be permanently revoked. The agent will lose trust-gated escrow access.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmRevoke(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              disabled={revokeMutation.isPending}
              onClick={() => {
                if (confirmRevoke) {
                  revokeMutation.mutate(confirmRevoke, {
                    onSuccess: () => setConfirmRevoke(null),
                  });
                }
              }}
            >
              {revokeMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Revoking...
                </>
              ) : (
                "Revoke Certificate"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Compliance Report Dialog */}
      <Dialog open={!!complianceId} onOpenChange={(open) => { if (!open) setComplianceId(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>EU AI Act Article 12 — Compliance Report</DialogTitle>
          </DialogHeader>
          <DialogBody>
            {compliance.isLoading ? (
              <div className="py-8 text-center text-sm text-muted-foreground">
                Generating compliance report...
              </div>
            ) : compliance.data?.report ? (
              <pre className="max-h-80 overflow-auto rounded-md border bg-background p-4 font-mono text-xs leading-relaxed text-muted-foreground">
                {JSON.stringify(compliance.data.report, null, 2)}
              </pre>
            ) : (
              <p className="text-sm text-muted-foreground">
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
        </DialogContent>
      </Dialog>
    </div>
  );
}
