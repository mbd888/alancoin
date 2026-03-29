import { cn } from "@/lib/utils";

function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}

// Precomputed widths for skeleton rows — avoids Math.random() in render
const SKELETON_WIDTHS = ["72%", "55%", "88%", "63%", "79%", "51%", "84%", "67%"];

/** Skeleton that matches a table row layout */
function SkeletonRow({ cols = 5 }: { cols?: number }) {
  return (
    <tr className="border-b border-border">
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
function SkeletonCard() {
  return (
    <div className="rounded-lg border bg-card p-5">
      <Skeleton className="mb-3 h-3 w-24" />
      <Skeleton className="mb-1 h-7 w-32" />
      <Skeleton className="h-3 w-16" />
    </div>
  );
}

export { Skeleton, SkeletonRow, SkeletonCard };
