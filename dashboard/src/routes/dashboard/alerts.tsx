import { useState } from "react";
import { ShieldAlert, CheckCircle, AlertTriangle, Info } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
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
  critical: { icon: ShieldAlert, variant: "danger" as const, color: "text-[var(--color-danger)]" },
  warning: { icon: AlertTriangle, variant: "warning" as const, color: "text-[var(--color-warning)]" },
  info: { icon: Info, variant: "default" as const, color: "text-[var(--foreground-muted)]" },
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
      <header className="border-b border-[var(--border)] px-8 py-5">
        <div className="flex items-center gap-2">
          <ShieldAlert size={18} strokeWidth={1.8} className="text-[var(--color-danger)]" />
          <h1 className="text-[16px] font-semibold text-[var(--foreground)]">
            Forensics Alerts
          </h1>
        </div>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Spend anomaly detection across all agents
        </p>
      </header>

      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={SEVERITY_TABS} active={severity} onChange={setSeverity} />
      </div>

      <div className="px-8 py-4">
        {alerts.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : alerts.data?.alerts?.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <CheckCircle size={32} strokeWidth={1.2} className="text-[var(--color-success)]" />
            <h3 className="mt-3 text-[14px] font-medium text-[var(--foreground)]">
              All clear
            </h3>
            <p className="mt-1 text-[13px] text-[var(--foreground-muted)]">
              No anomalies detected. Agent spending patterns are normal.
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {alerts.data?.alerts?.map((alert) => {
              const config = SEVERITY_CONFIG[alert.severity];
              const Icon = config.icon;
              return (
                <div
                  key={alert.id}
                  className="flex items-start gap-4 rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] px-5 py-4"
                >
                  <Icon size={16} strokeWidth={1.8} className={`mt-0.5 shrink-0 ${config.color}`} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <Badge variant={config.variant}>
                        {alert.severity}
                      </Badge>
                      <Badge variant="default">{alert.type.replace(/_/g, " ")}</Badge>
                      <span className="text-[11px] text-[var(--foreground-disabled)]">
                        {relativeTime(alert.detectedAt)}
                      </span>
                    </div>
                    <p className="mt-1 text-[13px] text-[var(--foreground-secondary)]">
                      {alert.message}
                    </p>
                    <div className="mt-2 flex items-center gap-4 text-[11px] tabular-nums text-[var(--foreground-muted)]">
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
                    <span className="text-[11px] text-[var(--foreground-disabled)]">
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
