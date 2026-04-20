import { useState, useMemo, useEffect } from "react";
import { cn } from "@/lib/utils";
import { SkeletonRow } from "./skeleton";
import { EmptyState } from "./empty-state";
import { ChevronLeft, ChevronRight, Database, ArrowUp, ArrowDown, ArrowUpDown } from "lucide-react";
import { Button } from "./button";
import type { ReactNode } from "react";

export interface Column<T> {
  id: string;
  header: string;
  cell: (row: T) => ReactNode;
  className?: string;
  numeric?: boolean;
  sortable?: boolean;
  sortValue?: (row: T) => string | number;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  isLoading?: boolean;
  emptyTitle?: string;
  emptyDescription?: string;
  emptyAction?: ReactNode;
  keyExtractor: (row: T) => string;
  onRowClick?: (row: T) => void;
  dataUpdatedAt?: number;
  // Pagination
  hasNextPage?: boolean;
  hasPrevPage?: boolean;
  onNextPage?: () => void;
  onPrevPage?: () => void;
  totalLabel?: string;
  page?: number;
}

function formatUpdatedAgo(timestamp: number): string {
  const diffSec = Math.floor((Date.now() - timestamp) / 1000);
  if (diffSec < 5) return "Updated just now";
  if (diffSec < 60) return `Updated ${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `Updated ${diffMin}m ago`;
  return `Updated ${Math.floor(diffMin / 60)}h ago`;
}

function FreshnessLabel({ dataUpdatedAt }: { dataUpdatedAt: number }) {
  const [, setTick] = useState(0);

  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 10_000);
    return () => clearInterval(id);
  }, []);

  return (
    <span className="text-xs text-muted-foreground/60">
      {formatUpdatedAgo(dataUpdatedAt)}
    </span>
  );
}

export function DataTable<T>({
  columns,
  data,
  isLoading = false,
  emptyTitle = "No data",
  emptyDescription = "There are no items to display.",
  emptyAction,
  keyExtractor,
  onRowClick,
  dataUpdatedAt,
  hasNextPage,
  hasPrevPage,
  onNextPage,
  onPrevPage,
  totalLabel,
  page,
}: DataTableProps<T>) {
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");

  const handleSort = (colId: string) => {
    if (sortColumn !== colId) {
      setSortColumn(colId);
      setSortDirection("asc");
    } else if (sortDirection === "asc") {
      setSortDirection("desc");
    } else {
      setSortColumn(null);
    }
  };

  const sortedData = useMemo(() => {
    if (!sortColumn) return data;
    const col = columns.find((c) => c.id === sortColumn);
    if (!col?.sortable || !col.sortValue) return data;

    const getValue = col.sortValue;
    return [...data].sort((a, b) => {
      const aVal = getValue(a);
      const bVal = getValue(b);
      let cmp: number;
      if (typeof aVal === "number" && typeof bVal === "number") {
        cmp = aVal - bVal;
      } else {
        cmp = String(aVal).localeCompare(String(bVal));
      }
      return sortDirection === "desc" ? -cmp : cmp;
    });
  }, [data, sortColumn, sortDirection, columns]);

  const showPagination = hasNextPage || hasPrevPage;
  const showFooter = showPagination || dataUpdatedAt || totalLabel;

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
                  {col.sortable ? (
                    <button
                      type="button"
                      onClick={() => handleSort(col.id)}
                      className={cn(
                        "inline-flex items-center gap-1 transition-colors hover:text-foreground",
                        col.numeric && "ml-auto"
                      )}
                    >
                      {col.header}
                      {sortColumn === col.id ? (
                        sortDirection === "asc" ? (
                          <ArrowUp size={12} className="text-foreground" />
                        ) : (
                          <ArrowDown size={12} className="text-foreground" />
                        )
                      ) : (
                        <ArrowUpDown size={12} className="opacity-40" />
                      )}
                    </button>
                  ) : (
                    col.header
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <SkeletonRow key={i} cols={columns.length} />
              ))
            ) : sortedData.length === 0 ? (
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
              sortedData.map((row) => (
                <tr
                  key={keyExtractor(row)}
                  className={cn(
                    "border-b transition-[background-color] duration-100",
                    onRowClick
                      ? "cursor-pointer hover:bg-accent/50"
                      : "hover:bg-card"
                  )}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
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

      {showFooter && (
        <div className="flex items-center justify-between border-t px-4 py-2.5">
          <div className="flex items-center gap-3">
            {totalLabel && (
              <span className="text-xs text-muted-foreground">
                {totalLabel}
                {page != null && (
                  <span className="ml-2 text-muted-foreground/60">
                    &middot; Page {page}
                  </span>
                )}
              </span>
            )}
            {dataUpdatedAt && <FreshnessLabel dataUpdatedAt={dataUpdatedAt} />}
          </div>
          {showPagination && (
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
          )}
        </div>
      )}
    </div>
  );
}
