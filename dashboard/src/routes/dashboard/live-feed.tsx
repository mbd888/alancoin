import { useState, useEffect, useRef } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/layouts/page-header";
import { Address } from "@/components/ui/address";
import { Rss } from "lucide-react";
import { useRealtimeStore } from "@/stores/realtime-store";
import { relativeTime } from "@/lib/utils";

const EVENT_VARIANTS: Record<string, string> = {
  transaction: "accent",
  proxy_settlement: "accent",
  session_created: "success",
  session_closed: "default",
  escrow_created: "warning",
  escrow_delivered: "success",
  escrow_confirmed: "success",
  escrow_disputed: "danger",
  stream_opened: "accent",
  stream_closed: "default",
  coalition: "accent",
  agent_joined: "success",
  milestone: "accent",
  price_alert: "warning",
};

const ALL_EVENT_TYPES = Object.keys(EVENT_VARIANTS);

function EventCard({ event }: { event: { type: string; timestamp: string; data: Record<string, unknown> } }) {
  const data = event.data;
  const from = (data.from ?? data.authorAddr ?? "") as string;
  const to = (data.to ?? "") as string;
  const amount = (data.amount ?? "") as string;

  return (
    <div className="flex items-center gap-4 border-b px-4 py-2.5 text-sm transition-[background-color] duration-150 hover:bg-accent">
      <Badge variant={(EVENT_VARIANTS[event.type] ?? "default") as "accent" | "success" | "default" | "warning" | "danger"}>
        {event.type}
      </Badge>
      <div className="flex-1 space-x-3">
        {from && <Address value={from} className="text-muted-foreground" />}
        {to && (
          <>
            <span className="text-muted-foreground/50">&rarr;</span>
            <Address value={to} className="text-muted-foreground" />
          </>
        )}
        {amount && (
          <span className="text-xs font-medium text-foreground">
            ${amount}
          </span>
        )}
      </div>
      <span className="text-xs text-muted-foreground/50">
        {relativeTime(event.timestamp)}
      </span>
    </div>
  );
}

export function LiveFeedPage() {
  const { connected, events, connect, disconnect, clearEvents } = useRealtimeStore();
  const [filters, setFilters] = useState<Set<string>>(new Set(ALL_EVENT_TYPES));
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  const toggleFilter = (type: string) => {
    setFilters((prev) => {
      const next = new Set(prev);
      if (next.has(type)) next.delete(type);
      else next.add(type);
      return next;
    });
  };

  const filteredEvents = events.filter((e) => filters.has(e.type));

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={Rss}
        title={
          <>
            Live Feed
            <span
              className="inline-block size-2 rounded-full"
              style={{ backgroundColor: connected ? "var(--color-success)" : "var(--color-danger)" }}
              title={connected ? "Connected" : "Disconnected"}
            />
          </>
        }
        description="Real-time event stream from WebSocket"
        actions={
          <Button variant="ghost" size="sm" onClick={clearEvents}>
            Clear
          </Button>
        }
      />

      {/* Filters */}
      <div className="flex flex-wrap gap-1.5 border-b px-4 md:px-8 py-3">
        {ALL_EVENT_TYPES.map((type) => (
          <button
            key={type}
            onClick={() => toggleFilter(type)}
            className={`rounded-full px-2.5 py-0.5 text-xs font-medium transition-colors ${
              filters.has(type)
                ? "bg-accent text-accent-foreground"
                : "bg-muted text-muted-foreground/50"
            }`}
          >
            {type.replace(/_/g, " ")}
          </button>
        ))}
      </div>

      {/* Event list */}
      <div ref={listRef} className="max-h-[calc(100vh-236px)] md:max-h-[calc(100vh-180px)] overflow-y-auto">
        {filteredEvents.length === 0 ? (
          <EmptyState
            icon={Rss}
            title={connected ? "Waiting for events..." : "Not connected"}
            description={connected ? "Events will appear here in real time." : "Attempting to establish WebSocket connection."}
          />
        ) : (
          filteredEvents.map((event, i) => <EventCard key={`${event.timestamp}-${i}`} event={event} />)
        )}
      </div>
    </div>
  );
}
