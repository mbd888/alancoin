import { cn } from "@/lib/utils";
import type { ButtonHTMLAttributes } from "react";

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
type ButtonSize = "sm" | "md" | "lg";

const variants: Record<ButtonVariant, string> = {
  primary:
    "bg-[var(--color-accent-5)] text-white hover:bg-[var(--color-accent-6)] active:bg-[var(--color-accent-4)]",
  secondary:
    "bg-[var(--background-interactive)] text-[var(--foreground-secondary)] border border-[var(--border)] hover:bg-[var(--color-gray-4)] hover:text-[var(--foreground)]",
  ghost:
    "text-[var(--foreground-secondary)] hover:bg-[var(--background-interactive)] hover:text-[var(--foreground)]",
  danger:
    "bg-[oklch(0.22_0.05_25)] text-[var(--color-danger)] border border-[oklch(0.30_0.06_25)] hover:bg-[oklch(0.26_0.06_25)]",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-[12px] gap-1.5",
  md: "h-8 px-3 text-[13px] gap-2",
  lg: "h-9 px-4 text-[14px] gap-2",
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

export function Button({
  variant = "secondary",
  size = "md",
  className,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      className={cn(
        "inline-flex items-center justify-center rounded-[var(--radius-md)] font-medium",
        "transition-[background-color,color,border-color] duration-150",
        "outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--background)]",
        "disabled:pointer-events-none disabled:opacity-40",
        variants[variant],
        sizes[size],
        className
      )}
      {...props}
    >
      {children}
    </button>
  );
}
