import type { UseQueryResult } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "./button";

/**
 * Handles loading/error/empty states for a TanStack Query result.
 * Avoids the common pattern of scattered ternaries in every page component.
 */
interface QueryStateProps<T> {
  query: UseQueryResult<T>;
  loading: ReactNode;
  children: (data: T) => ReactNode;
}

export function QueryState<T>({
  query,
  loading,
  children,
}: QueryStateProps<T>) {
  if (query.isLoading) return <>{loading}</>;

  if (query.isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
        <div className="flex size-10 items-center justify-center rounded-[var(--radius-lg)] bg-[oklch(0.18_0.03_25)]">
          <AlertTriangle size={18} strokeWidth={1.5} className="text-[var(--color-danger)]" />
        </div>
        <p className="text-[13px] text-[var(--foreground-muted)]">
          {query.error instanceof Error ? query.error.message : "Failed to load data"}
        </p>
        <Button variant="ghost" size="sm" onClick={() => query.refetch()}>
          <RefreshCw size={13} />
          Retry
        </Button>
      </div>
    );
  }

  if (!query.data) return <>{loading}</>;

  return <>{children(query.data)}</>;
}
