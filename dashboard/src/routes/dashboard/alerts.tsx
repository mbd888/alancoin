import { useState, useMemo } from "react";
import { ShieldAlert, CheckCircle, AlertTriangle, Info, Bell, Eye, MoreHorizontal } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton, SkeletonCard } from "@/components/ui/skeleton";
import { KpiCard } from "@/components/ui/kpi-card";
import { EmptyState } from "@/components/ui/empty-state";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { relativeTime } from "@/lib/utils";
import { Address } from "@/components/ui/address";
import { toast } from "sonner";

interface ForensicsAlert {
  id: string;
  agentAddr: string;
  type: string;
  severity: "info" | "warning" | "critical";
  message: string;
  score: number;
  baseline: number;
  actual: number;
  sigma: number;
  detectedAt: string;
  acknowledged: boolean;
}

const SEVERITY_TABS = [
  { id: "all", label: "All" },
  { id: "critical", label: "Critical" },
  { id: "warning", label: "Warning" },
  { id: "info", label: "Info" },
];

const SEVERITY_CONFIG = {
  critical: { icon: ShieldAlert, variant: "danger" as const, color: "text-destructive" },
  warning: { icon: AlertTriangle, variant: "warning" as const, color: "text-warning" },
  info: { icon: Info, variant: "default" as const, color: "text-muted-foreground" },
};

export function AlertsPage() {
  const [severity, setSeverity] = useState("all");
  const [viewAlert, setViewAlert] = useState<ForensicsAlert | null>(null);

  const alerts = useQuery({
    queryKey: ["forensics", "alerts"],
    queryFn: () =>
      api.get<{ alerts: ForensicsAlert[]; count: number }>(
        "/forensics/alerts",
        { limit: "100" }
      ),
  });

  const handleAcknowledge = async (alertId: string) => {
    try {
      await api.post(`/forensics/alerts/${alertId}/acknowledge`);
      toast.success("Alert acknowledged");
      alerts.refetch();
    } catch {
      toast.error("Failed to acknowledge alert");
    }
  };

  const allAlerts = alerts.data?.alerts ?? [];

  const filteredAlerts = useMemo(() => {
    if (severity === "all") return allAlerts;
    return allAlerts.filter((a) => a.severity === severity);
  }, [allAlerts, severity]);

  const counts = useMemo(() => {
    const m: Record<string, number> = { all: allAlerts.length, critical: 0, warning: 0, info: 0 };
    for (const a of allAlerts) {
      m[a.severity] = (m[a.severity] ?? 0) + 1;
    }
    return m;
  }, [allAlerts]);

  const unackedCount = useMemo(
    () => allAlerts.filter((a) => !a.acknowledged).length,
    [allAlerts]
  );

  const tabsWithCounts = SEVERITY_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  return (
    <div className="min-h-screen">
      <PageHeader icon={ShieldAlert} title="Forensics Alerts" description="Spend anomaly detection across all agents" />

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 border-b px-4 md:px-8 py-4">
        {alerts.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <KpiCard icon={Bell} label="Total Alerts" value={allAlerts.length} />
            <KpiCard icon={ShieldAlert} label="Critical" value={counts.critical} />
            <KpiCard icon={AlertTriangle} label="Warning" value={counts.warning} />
            <KpiCard icon={CheckCircle} label="Unacknowledged" value={unackedCount} />
          </>
        )}
      </div>

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={severity} onChange={setSeverity} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {alerts.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : alerts.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load alerts
            <Button variant="ghost" size="sm" onClick={() => alerts.refetch()}>
              Retry
            </Button>
          </div>
        ) : filteredAlerts.length === 0 ? (
          <EmptyState
            icon={CheckCircle}
            title={severity === "all" ? "All clear" : `No ${severity} alerts`}
            description={severity === "all" ? "No anomalies detected. Agent spending patterns are normal." : `No alerts with ${severity} severity found.`}
          />
        ) : (
          <div className="flex flex-col gap-2">
            {filteredAlerts.map((alert) => {
              const config = SEVERITY_CONFIG[alert.severity];
              const Icon = config.icon;
              return (
                <div
                  key={alert.id}
                  className="flex cursor-pointer items-start gap-4 rounded-lg border bg-card px-5 py-4 transition-colors hover:bg-accent/30"
                  onClick={() => setViewAlert(alert)}
                >
                  <Icon size={16} strokeWidth={1.8} className={`mt-0.5 shrink-0 ${config.color}`} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <Badge variant={config.variant}>
                        {alert.severity}
                      </Badge>
                      <Badge variant="default">{alert.type.replace(/_/g, " ")}</Badge>
                      <span className="text-xs text-muted-foreground/50">
                        {relativeTime(alert.detectedAt)}
                      </span>
                    </div>
                    <p className="mt-1 text-sm text-muted-foreground">
                      {alert.message}
                    </p>
                    <div className="mt-2 flex items-center gap-4 text-xs tabular-nums text-muted-foreground">
                      <span>Score: {alert.score.toFixed(1)}</span>
                      {alert.sigma > 0 && <span>Sigma: {alert.sigma.toFixed(1)}σ</span>}
                      <span>Baseline: {alert.baseline.toFixed(2)}</span>
                      <span>Actual: {alert.actual.toFixed(2)}</span>
                      <Address value={alert.agentAddr} />
                    </div>
                  </div>
                  <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
                    {!alert.acknowledged && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleAcknowledge(alert.id)}
                      >
                        Acknowledge
                      </Button>
                    )}
                    {alert.acknowledged && (
                      <span className="text-xs text-muted-foreground/50">
                        Acknowledged
                      </span>
                    )}
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button aria-label="Alert actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                          <MoreHorizontal size={15} />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setViewAlert(alert)}>
                          <Eye size={13} />
                          View details
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <Dialog open={!!viewAlert} onOpenChange={() => setViewAlert(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Alert Details</DialogTitle>
            <DialogDescription>Forensic anomaly detection breakdown.</DialogDescription>
          </DialogHeader>
          {viewAlert && (() => {
            const config = SEVERITY_CONFIG[viewAlert.severity];
            return (
              <DialogBody>
                <div className="flex flex-col gap-3 text-sm">
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Alert ID</span>
                    <code className="text-right font-mono text-xs">{viewAlert.id}</code>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Agent</span>
                    <Address value={viewAlert.agentAddr} truncate={false} />
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Severity</span>
                    <Badge variant={config.variant}>{viewAlert.severity}</Badge>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Type</span>
                    <span>{viewAlert.type.replace(/_/g, " ")}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Status</span>
                    <Badge variant={viewAlert.acknowledged ? "default" : "warning"}>
                      {viewAlert.acknowledged ? "acknowledged" : "unacknowledged"}
                    </Badge>
                  </div>

                  <hr className="border-border" />
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Message</span>
                    <span className="text-right text-xs">{viewAlert.message}</span>
                  </div>

                  <hr className="border-border" />
                  <p className="text-xs font-medium text-muted-foreground">Statistical Analysis</p>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Anomaly Score</span>
                    <span className="tabular-nums font-medium">{viewAlert.score.toFixed(2)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Baseline</span>
                    <span className="tabular-nums">{viewAlert.baseline.toFixed(4)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Actual</span>
                    <span className="tabular-nums">{viewAlert.actual.toFixed(4)}</span>
                  </div>
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Deviation</span>
                    <span className="tabular-nums">
                      {viewAlert.sigma > 0 ? `${viewAlert.sigma.toFixed(2)}σ` : "—"}
                    </span>
                  </div>

                  <hr className="border-border" />
                  <div className="flex items-start justify-between gap-4">
                    <span className="text-xs text-muted-foreground">Detected</span>
                    <span>{relativeTime(viewAlert.detectedAt)}</span>
                  </div>
                </div>
              </DialogBody>
            );
          })()}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewAlert(null)}>
              Close
            </Button>
            {viewAlert && !viewAlert.acknowledged && (
              <Button
                variant="primary"
                size="sm"
                onClick={() => {
                  handleAcknowledge(viewAlert.id);
                  setViewAlert(null);
                }}
              >
                Acknowledge
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
