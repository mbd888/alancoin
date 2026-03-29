import { useState } from "react";
import { ShieldAlert, CheckCircle, AlertTriangle, Info } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { relativeTime } from "@/lib/utils";
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

  const alerts = useQuery({
    queryKey: ["forensics", "alerts", severity],
    queryFn: () => {
      const params: Record<string, string> = { limit: "100" };
      if (severity !== "all") params.severity = severity;
      return api.get<{ alerts: ForensicsAlert[]; count: number }>(
        "/forensics/alerts",
        params
      );
    },
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

  return (
    <div className="min-h-screen">
      <PageHeader icon={ShieldAlert} title="Forensics Alerts" description="Spend anomaly detection across all agents" />

      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={SEVERITY_TABS} active={severity} onChange={setSeverity} />
      </div>

      <div className="px-4 md:px-8 py-4">
        {alerts.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : alerts.data?.alerts?.length === 0 ? (
          <EmptyState
            icon={CheckCircle}
            title="All clear"
            description="No anomalies detected. Agent spending patterns are normal."
          />
        ) : (
          <div className="flex flex-col gap-2">
            {alerts.data?.alerts?.map((alert) => {
              const config = SEVERITY_CONFIG[alert.severity];
              const Icon = config.icon;
              return (
                <div
                  key={alert.id}
                  className="flex items-start gap-4 rounded-lg border bg-card px-5 py-4"
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
                      <span className="font-mono">{alert.agentAddr.slice(0, 10)}...</span>
                    </div>
                  </div>
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
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
