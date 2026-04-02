import { useState, useMemo, useEffect } from "react";
import { MoreHorizontal, Eye, XCircle, DollarSign, Radio } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQueryClient } from "@tanstack/react-query";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { useSessions } from "@/hooks/api/use-dashboard";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { useRealtimeStore } from "@/stores/realtime-store";
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
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button aria-label="Session actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
          <MoreHorizontal size={15} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => toast.info(`Session ${session.id.slice(0, 8)}...`)}>
          <Eye size={13} />
          View details
        </DropdownMenuItem>
        {session.status === "active" && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem danger onClick={() => toast.info("Session close coming soon")}>
              <XCircle size={13} />
              Close session
            </DropdownMenuItem>
          </>
        )}
        {session.status === "exhausted" && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => toast.info("Settlement coming soon")}>
              <DollarSign size={13} />
              Settle now
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function SessionsPage() {
  const [cursor, setCursor] = useState<string | undefined>();
  const [history, setHistory] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState("all");
  const sessions = useSessions(50, cursor);
  const queryClient = useQueryClient();
  const { events, connect, disconnect } = useRealtimeStore();

  // Auto-refresh sessions when relevant WebSocket events arrive
  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  useEffect(() => {
    const last = events[0];
    if (last && (last.type === "session_created" || last.type === "session_closed" || last.type === "proxy_settlement")) {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "sessions"] });
    }
  }, [events, queryClient]);

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
        <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "agent",
      header: "Agent",
      cell: (row) => (
        <span className="font-mono text-xs">
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
      cell: (row) => <span className="text-xs">{row.strategy}</span>,
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
            <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
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
            <span className="text-xs">
              {formatCurrency(row.totalSpent)}
              <span className="text-muted-foreground/50"> / </span>
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
        <span className="text-xs text-muted-foreground">
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
      <PageHeader icon={Radio} title="Sessions" description="Gateway payment sessions" />

      {/* Toolbar: status filter tabs */}
      <div className="border-b px-4 md:px-8 py-3">
        <Tabs tabs={tabsWithCounts} active={statusFilter} onChange={setStatusFilter} />
      </div>

      <div className="px-4 md:px-8 py-4">
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
