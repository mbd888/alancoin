import { cn } from "@/lib/utils";

export function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "animate-pulse rounded-[var(--radius-md)] bg-[var(--color-gray-3)]",
        className
      )}
      {...props}
    />
  );
}

// Precomputed widths for skeleton rows — avoids Math.random() in render
const SKELETON_WIDTHS = ["72%", "55%", "88%", "63%", "79%", "51%", "84%", "67%"];

/** Skeleton that matches a table row layout */
export function SkeletonRow({ cols = 5 }: { cols?: number }) {
  return (
    <tr className="border-b border-[var(--border-subtle)]">
      {Array.from({ length: cols }).map((_, i) => (
        <td key={i} className="px-4 py-3">
          <Skeleton
            className="h-4"
            style={{ width: SKELETON_WIDTHS[i % SKELETON_WIDTHS.length] }}
          />
        </td>
      ))}
    </tr>
  );
}

/** Skeleton matching a KPI card */
export function SkeletonCard() {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[var(--border-subtle)] bg-[var(--background-elevated)] p-5">
      <Skeleton className="mb-3 h-3 w-24" />
      <Skeleton className="mb-1 h-7 w-32" />
      <Skeleton className="h-3 w-16" />
    </div>
  );
}
