import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: ReactNode;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
}: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="mb-4 flex size-12 items-center justify-center rounded-[var(--radius-lg)] bg-[var(--background-interactive)]">
        <Icon size={22} strokeWidth={1.5} className="text-[var(--foreground-muted)]" />
      </div>
      <h3 className="text-[14px] font-medium text-[var(--foreground)]">
        {title}
      </h3>
      <p className="mt-1 max-w-sm text-[13px] text-[var(--foreground-muted)]">
        {description}
      </p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
