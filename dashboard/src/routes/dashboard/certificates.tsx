import { useState } from "react";
import { Shield, Plus, CheckCircle, XCircle, FileText, ExternalLink, Loader2, AlertTriangle, Users } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { relativeTime } from "@/lib/utils";
import { Skeleton } from "@/components/ui/skeleton";
import { KpiCard } from "@/components/ui/kpi-card";
import { SkeletonCard } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { Address } from "@/components/ui/address";
import { toast } from "sonner";
import { useCertificates, useComplianceReport, useIssueCertificate, useRevokeCertificate } from "@/hooks/api/use-certificates";
import type { KYACertificate } from "@/hooks/api/use-certificates";

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
  const [viewCert, setViewCert] = useState<KYACertificate | null>(null);

  const certs = useCertificates();
  const compliance = useComplianceReport(complianceId);

  const issueMutation = useIssueCertificate(() => {
    setIssueOpen(false);
    setIssueAddr("");
    setIssueOrg("");
    setIssueDept("");
  });

  const revokeMutation = useRevokeCertificate();

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

      {(() => {
        const allCerts = certs.data?.certificates ?? [];
        const activeCount = allCerts.filter((c) => c.status === "active").length;
        const revokedCount = allCerts.filter((c) => c.status === "revoked").length;
        const avgScore = allCerts.length > 0
          ? allCerts.reduce((sum, c) => sum + c.reputation.traceRankScore, 0) / allCerts.length
          : 0;
        return (
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
            {certs.isLoading ? (
              Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
            ) : (
              <>
                <KpiCard icon={Shield} label="Total Certificates" value={allCerts.length} />
                <KpiCard icon={CheckCircle} label="Active" value={activeCount} />
                <KpiCard icon={XCircle} label="Revoked" value={revokedCount} />
                <KpiCard icon={Users} label="Avg TraceRank" value={avgScore.toFixed(1)} />
              </>
            )}
          </div>
        );
      })()}

      <div className="px-4 md:px-8 py-6">
        {certs.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-24" />
            ))}
          </div>
        ) : certs.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load certificates
            <Button variant="ghost" size="sm" onClick={() => certs.refetch()}>
              Retry
            </Button>
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
                className="cursor-pointer rounded-lg border bg-card p-5 transition-colors hover:bg-accent/30"
                onClick={() => setViewCert(cert)}
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
                      <Address value={cert.agentAddr} />
                      <span>Org: {cert.org.orgName}</span>
                      {cert.org.department && <span>Dept: {cert.org.department}</span>}
                      <span>Issued {relativeTime(cert.issuedAt)}</span>
                    </div>
                    <div className="mt-2 flex items-center gap-4 text-xs tabular-nums text-muted-foreground">
                      <span>Score: {cert.reputation.traceRankScore.toFixed(1)}</span>
                      <span>Success: {(cert.reputation.successRate * 100).toFixed(0)}%</span>
                      <span>Disputes: {(cert.reputation.disputeRate * 100).toFixed(1)}%</span>
                      <span>Txns: {cert.reputation.txCount}</span>
                      <span>Age: {cert.reputation.accountAgeDays}d</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
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
            <DialogDescription>
              Verify agent identity and enable trust-gated escrow access.
            </DialogDescription>
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

      {/* Certificate Detail Dialog */}
      <Dialog open={!!viewCert} onOpenChange={() => setViewCert(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Certificate Details</DialogTitle>
            <DialogDescription>KYA certificate identity and reputation data.</DialogDescription>
          </DialogHeader>
          {viewCert && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Certificate ID</span>
                  <code className="text-right font-mono text-xs">{viewCert.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">DID</span>
                  <code className="text-right font-mono text-xs">{viewCert.did}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Agent</span>
                  <Address value={viewCert.agentAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={viewCert.status === "active" ? "success" : "danger"}>
                    {viewCert.status}
                  </Badge>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Trust Tier</span>
                  <Badge variant={TIER_VARIANT[viewCert.reputation.trustTier] ?? "default"}>
                    {viewCert.reputation.trustTier}
                  </Badge>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Organization</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Name</span>
                  <span>{viewCert.org.orgName}</span>
                </div>
                {viewCert.org.department && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Department</span>
                    <span>{viewCert.org.department}</span>
                  </div>
                )}
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Authorized By</span>
                  <span>{viewCert.org.authorizedBy}</span>
                </div>

                <hr className="border-border" />
                <p className="text-xs font-medium text-muted-foreground">Reputation</p>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">TraceRank Score</span>
                  <span className="tabular-nums">{viewCert.reputation.traceRankScore.toFixed(1)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Success Rate</span>
                  <span className="tabular-nums">{(viewCert.reputation.successRate * 100).toFixed(1)}%</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Dispute Rate</span>
                  <span className="tabular-nums">{(viewCert.reputation.disputeRate * 100).toFixed(1)}%</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Transactions</span>
                  <span className="tabular-nums">{viewCert.reputation.txCount.toLocaleString()}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Account Age</span>
                  <span className="tabular-nums">{viewCert.reputation.accountAgeDays} days</span>
                </div>

                {viewCert.permissions.maxSpendPerDay && (
                  <>
                    <hr className="border-border" />
                    <p className="text-xs font-medium text-muted-foreground">Permissions</p>
                    <div className="flex items-start justify-between gap-4">
                      <span className="text-xs text-muted-foreground">Max Spend / Day</span>
                      <span className="tabular-nums">${viewCert.permissions.maxSpendPerDay}</span>
                    </div>
                    {viewCert.permissions.allowedApis && viewCert.permissions.allowedApis.length > 0 && (
                      <div className="flex items-start justify-between gap-4">
                        <span className="text-xs text-muted-foreground">Allowed APIs</span>
                        <span className="text-right text-xs">{viewCert.permissions.allowedApis.join(", ")}</span>
                      </div>
                    )}
                  </>
                )}

                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Issued</span>
                  <span>{relativeTime(viewCert.issuedAt)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Expires</span>
                  <span>{relativeTime(viewCert.expiresAt)}</span>
                </div>
                {viewCert.revokedAt && (
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Revoked</span>
                    <span>{relativeTime(viewCert.revokedAt)}</span>
                  </div>
                )}
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewCert(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Compliance Report Dialog */}
      <Dialog open={!!complianceId} onOpenChange={(open) => { if (!open) setComplianceId(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>EU AI Act Article 12 — Compliance Report</DialogTitle>
            <DialogDescription>
              Machine-readable traceability record for this agent's certificate.
            </DialogDescription>
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
