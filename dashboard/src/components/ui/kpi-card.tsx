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
    <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
      <div className="flex items-center justify-between">
        <span className="text-[12px] font-medium text-[var(--foreground-muted)]">
          {label}
        </span>
        {Icon && (
          <Icon
            size={15}
            strokeWidth={1.5}
            className="text-[var(--foreground-disabled)]"
          />
        )}
      </div>
      <div className="mt-2 text-[22px] font-semibold tabular-nums text-[var(--foreground)]">
        {value}
      </div>
      {change && (
        <span
          className={cn(
            "mt-1 inline-block text-[12px] font-medium tabular-nums",
            changeType === "positive" && "text-[var(--color-success)]",
            changeType === "negative" && "text-[var(--color-danger)]",
            changeType === "neutral" && "text-[var(--foreground-muted)]"
          )}
        >
          {change}
        </span>
      )}
    </div>
  );
}
