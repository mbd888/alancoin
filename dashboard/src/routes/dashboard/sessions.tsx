import { useState, useMemo, useEffect } from "react";
import { MoreHorizontal, Eye, XCircle, DollarSign, Radio, Loader2, AlertTriangle } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { useQueryClient, useMutation } from "@tanstack/react-query";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { useSessions } from "@/hooks/api/use-dashboard";
import { api } from "@/lib/api-client";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { useRealtimeStore } from "@/stores/realtime-store";
import { Address } from "@/components/ui/address";
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

function SessionActions({
  session,
  onClose,
  onViewDetails,
}: {
  session: GatewaySession;
  onClose: () => void;
  onViewDetails: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button aria-label="Session actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
          <MoreHorizontal size={15} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={onViewDetails}>
          <Eye size={13} />
          View details
        </DropdownMenuItem>
        {session.status === "active" && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem danger onClick={onClose}>
              <XCircle size={13} />
              Close session
            </DropdownMenuItem>
          </>
        )}
        {session.status === "exhausted" && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem disabled>
              <DollarSign size={13} />
              Settlement is automatic
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
  const [confirmClose, setConfirmClose] = useState<GatewaySession | null>(null);
  const [viewSession, setViewSession] = useState<GatewaySession | null>(null);
  const sessions = useSessions(50, cursor);
  const queryClient = useQueryClient();
  const { events, connect, disconnect } = useRealtimeStore();

  const closeMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/gateway/sessions/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "sessions"] });
      setConfirmClose(null);
      toast.success("Session closed");
    },
    onError: () => toast.error("Failed to close session"),
  });

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
      cell: (row) => <Address value={row.agentAddr} />,
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
      sortable: true,
      sortValue: (row) => parseFloat(row.totalSpent) || 0,
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
      sortable: true,
      sortValue: (row) => row.requestCount,
      cell: (row) => row.requestCount,
    },
    {
      id: "created",
      header: "Created",
      sortable: true,
      sortValue: (row) => new Date(row.createdAt).getTime(),
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
      cell: (row) => (
        <div onClick={(e) => e.stopPropagation()}>
          <SessionActions session={row} onClose={() => setConfirmClose(row)} onViewDetails={() => setViewSession(row)} />
        </div>
      ),
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
        {sessions.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load sessions
            <Button variant="ghost" size="sm" onClick={() => sessions.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={filteredSessions}
            isLoading={sessions.isLoading}
            keyExtractor={(row) => row.id}
            onRowClick={(row) => setViewSession(row)}
            dataUpdatedAt={sessions.dataUpdatedAt}
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
            page={history.length + 1}
          />
        )}
      </div>

      {/* Session Details Dialog */}
      <Dialog open={!!viewSession} onOpenChange={() => setViewSession(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Session Details</DialogTitle>
            <DialogDescription>
              Gateway payment session information.
            </DialogDescription>
          </DialogHeader>
          {viewSession && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Session ID</span>
                  <code className="text-right font-mono text-xs">{viewSession.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Agent</span>
                  <code className="font-mono text-xs">{viewSession.agentAddr}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={STATUS_VARIANT[viewSession.status] ?? "default"}>{viewSession.status}</Badge>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Strategy</span>
                  <span>{viewSession.strategy}</span>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Budget</span>
                  <span className="tabular-nums">{formatCurrency(viewSession.totalSpent)} / {formatCurrency(viewSession.maxTotal)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Max Per Request</span>
                  <span className="tabular-nums">{formatCurrency(viewSession.maxPerRequest)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Requests</span>
                  <span className="tabular-nums">{viewSession.requestCount}</span>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewSession.createdAt)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Expires</span>
                  <span>{relativeTime(viewSession.expiresAt)}</span>
                </div>
                {viewSession.allowedTypes?.length > 0 && (
                  <>
                    <hr className="border-border" />
                    <div>
                      <span className="text-xs text-muted-foreground">Allowed Types</span>
                      <div className="mt-1 flex flex-wrap gap-1">
                        {viewSession.allowedTypes.map((t) => (
                          <Badge key={t} variant="default" className="text-[10px]">{t}</Badge>
                        ))}
                      </div>
                    </div>
                  </>
                )}
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewSession(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Close Session Confirmation */}
      <Dialog open={!!confirmClose} onOpenChange={() => setConfirmClose(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Close Session</DialogTitle>
            <DialogDescription>
              This will close the session and refund any remaining budget. Active requests may fail.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmClose(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              disabled={closeMutation.isPending}
              onClick={() => confirmClose && closeMutation.mutate(confirmClose.id)}
            >
              {closeMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Closing...
                </>
              ) : (
                "Close Session"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
