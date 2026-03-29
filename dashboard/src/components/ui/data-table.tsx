import { cn } from "@/lib/utils";
import { SkeletonRow } from "./skeleton";
import { EmptyState } from "./empty-state";
import { ChevronLeft, ChevronRight, Database } from "lucide-react";
import { Button } from "./button";
import type { ReactNode } from "react";

export interface Column<T> {
  id: string;
  header: string;
  cell: (row: T) => ReactNode;
  className?: string;
  numeric?: boolean;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  isLoading?: boolean;
  emptyTitle?: string;
  emptyDescription?: string;
  emptyAction?: ReactNode;
  keyExtractor: (row: T) => string;
  // Pagination
  hasNextPage?: boolean;
  hasPrevPage?: boolean;
  onNextPage?: () => void;
  onPrevPage?: () => void;
  totalLabel?: string;
}

export function DataTable<T>({
  columns,
  data,
  isLoading = false,
  emptyTitle = "No data",
  emptyDescription = "There are no items to display.",
  emptyAction,
  keyExtractor,
  hasNextPage,
  hasPrevPage,
  onNextPage,
  onPrevPage,
  totalLabel,
}: DataTableProps<T>) {
  const showPagination = hasNextPage || hasPrevPage;

  return (
    <div className="flex flex-col">
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b">
              {columns.map((col) => (
                <th
                  key={col.id}
                  className={cn(
                    "sticky top-0 z-10 bg-background px-4 py-2.5",
                    "text-xs font-medium uppercase tracking-wide text-muted-foreground",
                    col.numeric && "text-right",
                    col.className
                  )}
                >
                  {col.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <SkeletonRow key={i} cols={columns.length} />
              ))
            ) : data.length === 0 ? (
              <tr>
                <td colSpan={columns.length}>
                  <EmptyState
                    icon={Database}
                    title={emptyTitle}
                    description={emptyDescription}
                    action={emptyAction}
                  />
                </td>
              </tr>
            ) : (
              data.map((row) => (
                <tr
                  key={keyExtractor(row)}
                  className="border-b transition-[background-color] duration-100 hover:bg-card"
                >
                  {columns.map((col) => (
                    <td
                      key={col.id}
                      className={cn(
                        "px-4 py-2.5 text-sm text-muted-foreground",
                        col.numeric && "text-right tabular-nums",
                        col.className
                      )}
                    >
                      {col.cell(row)}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {showPagination && (
        <div className="flex items-center justify-between border-t px-4 py-2.5">
          <span className="text-xs text-muted-foreground">
            {totalLabel ?? `${data.length} items`}
          </span>
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              disabled={!hasPrevPage}
              onClick={onPrevPage}
            >
              <ChevronLeft size={14} />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={!hasNextPage}
              onClick={onNextPage}
            >
              <ChevronRight size={14} />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
