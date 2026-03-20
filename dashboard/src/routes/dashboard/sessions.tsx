import { useState, useMemo } from "react";
import { MoreHorizontal, Eye, XCircle, DollarSign } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { DropdownMenu, DropdownItem, DropdownSeparator } from "@/components/ui/dropdown-menu";
import { useSessions } from "@/hooks/api/use-dashboard";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import type { GatewaySession } from "@/lib/types";

const STATUS_VARIANT = {
  active: "success",
  exhausted: "warning",
  expired: "default",
  settled: "accent",
} as const;

const STATUS_TABS = [
  { id: "all", label: "All" },
  { id: "active", label: "Active" },
  { id: "exhausted", label: "Exhausted" },
  { id: "settled", label: "Settled" },
  { id: "expired", label: "Expired" },
];

function SessionActions({ session }: { session: GatewaySession }) {
  return (
    <DropdownMenu
      trigger={
        <button className="rounded-[var(--radius-sm)] p-1 text-[var(--foreground-disabled)] transition-[color,background-color] duration-150 hover:bg-[var(--background-interactive)] hover:text-[var(--foreground-secondary)]">
          <MoreHorizontal size={15} />
        </button>
      }
    >
      <DropdownItem onClick={() => toast.info(`Session ${session.id.slice(0, 8)}...`)}>
        <Eye size={13} />
        View details
      </DropdownItem>
      {session.status === "active" && (
        <>
          <DropdownSeparator />
          <DropdownItem danger onClick={() => toast.info("Session close coming soon")}>
            <XCircle size={13} />
            Close session
          </DropdownItem>
        </>
      )}
      {session.status === "exhausted" && (
        <>
          <DropdownSeparator />
          <DropdownItem onClick={() => toast.info("Settlement coming soon")}>
            <DollarSign size={13} />
            Settle now
          </DropdownItem>
        </>
      )}
    </DropdownMenu>
  );
}

export function SessionsPage() {
  const [cursor, setCursor] = useState<string | undefined>();
  const [history, setHistory] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState("all");
  const sessions = useSessions(50, cursor);

  const filteredSessions = useMemo(() => {
    const all = sessions.data?.sessions ?? [];
    if (statusFilter === "all") return all;
    return all.filter((s) => s.status === statusFilter);
  }, [sessions.data?.sessions, statusFilter]);

  // Compute counts per status for tab badges
  const counts = useMemo(() => {
    const all = sessions.data?.sessions ?? [];
    const map: Record<string, number> = { all: all.length };
    for (const s of all) {
      map[s.status] = (map[s.status] ?? 0) + 1;
    }
    return map;
  }, [sessions.data?.sessions]);

  const tabsWithCounts = STATUS_TABS.map((t) => ({
    ...t,
    count: counts[t.id] ?? 0,
  }));

  const columns: Column<GatewaySession>[] = [
    {
      id: "id",
      header: "Session",
      cell: (row) => (
        <span className="font-mono text-[12px]">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "agent",
      header: "Agent",
      cell: (row) => (
        <span className="font-mono text-[12px]">
          {row.agentAddr.slice(0, 8)}...{row.agentAddr.slice(-4)}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge variant={STATUS_VARIANT[row.status] ?? "default"}>{row.status}</Badge>
      ),
    },
    {
      id: "strategy",
      header: "Strategy",
      cell: (row) => <span className="text-[12px]">{row.strategy}</span>,
    },
    {
      id: "budget",
      header: "Budget Usage",
      numeric: true,
      cell: (row) => {
        const spent = parseFloat(row.totalSpent) || 0;
        const total = parseFloat(row.maxTotal) || 1;
        const pct = Math.min((spent / total) * 100, 100);
        return (
          <div className="flex items-center gap-3">
            <div className="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--color-gray-3)]">
              <div
                className="h-full rounded-full transition-[width] duration-300"
                style={{
                  width: `${pct}%`,
                  backgroundColor:
                    pct >= 90
                      ? "var(--color-danger)"
                      : pct >= 70
                        ? "var(--color-warning)"
                        : "var(--color-accent-6)",
                }}
              />
            </div>
            <span className="text-[12px]">
              {formatCurrency(row.totalSpent)}
              <span className="text-[var(--foreground-disabled)]"> / </span>
              {formatCurrency(row.maxTotal)}
            </span>
          </div>
        );
      },
    },
    {
      id: "requests",
      header: "Reqs",
      numeric: true,
      cell: (row) => row.requestCount,
    },
    {
      id: "created",
      header: "Created",
      cell: (row) => (
        <span className="text-[12px] text-[var(--foreground-muted)]">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      className: "w-10",
      cell: (row) => <SessionActions session={row} />,
    },
  ];

  const handleNext = () => {
    if (sessions.data?.next_cursor) {
      setHistory((h) => [...h, cursor ?? ""]);
      setCursor(sessions.data.next_cursor);
    }
  };

  const handlePrev = () => {
    const prev = history[history.length - 1];
    setHistory((h) => h.slice(0, -1));
    setCursor(prev || undefined);
  };

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Sessions</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Gateway payment sessions
        </p>
      </header>

      {/* Toolbar: status filter tabs */}
      <div className="border-b border-[var(--border)] px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={filteredSessions}
          isLoading={sessions.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle={statusFilter === "all" ? "No sessions" : `No ${statusFilter} sessions`}
          emptyDescription={
            statusFilter === "all"
              ? "No gateway sessions have been created yet."
              : `No sessions with status "${statusFilter}" found.`
          }
          hasNextPage={sessions.data?.has_more}
          hasPrevPage={history.length > 0}
          onNextPage={handleNext}
          onPrevPage={handlePrev}
          totalLabel={`${filteredSessions.length} sessions`}
        />
      </div>
    </div>
  );
}
