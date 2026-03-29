import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";

interface KpiCardProps {
  label: string;
  value: string | number;
  change?: string;
  changeType?: "positive" | "negative" | "neutral";
  icon?: LucideIcon;
}

export function KpiCard({
  label,
  value,
  change,
  changeType = "neutral",
  icon: Icon,
}: KpiCardProps) {
  return (
    <div className="rounded-lg border bg-card p-5">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">
          {label}
        </span>
        {Icon && (
          <Icon
            size={15}
            strokeWidth={1.5}
            className="text-muted-foreground/50"
          />
        )}
      </div>
      <div className="mt-2 text-xl font-semibold tabular-nums text-foreground">
        {value}
      </div>
      {change && (
        <span
          className={cn(
            "mt-1 inline-block text-xs font-medium tabular-nums",
            changeType === "positive" && "text-success",
            changeType === "negative" && "text-destructive",
            changeType === "neutral" && "text-muted-foreground"
          )}
        >
          {change}
        </span>
      )}
    </div>
  );
}
