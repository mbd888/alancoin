import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

type BadgeVariant = "default" | "success" | "warning" | "danger" | "accent";

const badgeVariants = cva(
  "inline-flex items-center rounded-sm px-1.5 py-0.5 text-xs font-medium",
  {
    variants: {
      variant: {
        default:
          "bg-secondary text-secondary-foreground",
        success:
          "bg-[oklch(0.25_0.05_145)] text-success",
        warning:
          "bg-[oklch(0.25_0.04_75)] text-warning",
        danger:
          "bg-destructive/15 text-destructive",
        accent:
          "bg-accent-2 text-accent-7",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
);

interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

export { Badge, badgeVariants };
export type { BadgeProps, BadgeVariant };
