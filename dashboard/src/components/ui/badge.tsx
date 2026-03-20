import { cn } from "@/lib/utils";

type BadgeVariant = "default" | "success" | "warning" | "danger" | "accent";

const variantStyles: Record<BadgeVariant, string> = {
  default:
    "bg-[var(--color-gray-3)] text-[var(--foreground-secondary)]",
  success:
    "bg-[oklch(0.25_0.05_145)] text-[var(--color-success)]",
  warning:
    "bg-[oklch(0.25_0.04_75)] text-[var(--color-warning)]",
  danger:
    "bg-[oklch(0.22_0.05_25)] text-[var(--color-danger)]",
  accent:
    "bg-[var(--color-accent-2)] text-[var(--color-accent-7)]",
};

interface BadgeProps {
  variant?: BadgeVariant;
  children: React.ReactNode;
  className?: string;
}

export function Badge({
  variant = "default",
  children,
  className,
}: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-[var(--radius-sm)] px-1.5 py-0.5 text-[11px] font-medium",
        variantStyles[variant],
        className
      )}
    >
      {children}
    </span>
  );
}
