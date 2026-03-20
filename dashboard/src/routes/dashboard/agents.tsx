import { Bot, Plus, MoreHorizontal, Eye, Trash2, Zap } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownItem, DropdownSeparator } from "@/components/ui/dropdown-menu";
import { useAgents } from "@/hooks/api/use-agents";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import type { Agent } from "@/lib/types";

function AgentActions({ agent }: { agent: Agent }) {
  return (
    <DropdownMenu
      trigger={
        <button className="rounded-[var(--radius-sm)] p-1 text-[var(--foreground-disabled)] transition-[color,background-color] duration-150 hover:bg-[var(--background-interactive)] hover:text-[var(--foreground-secondary)]">
          <MoreHorizontal size={15} />
        </button>
      }
    >
      <DropdownItem onClick={() => toast.info(`Agent: ${agent.address}`)}>
        <Eye size={13} />
        View details
      </DropdownItem>
      <DropdownItem onClick={() => toast.info("Add service coming soon")}>
        <Zap size={13} />
        Add service
      </DropdownItem>
      <DropdownSeparator />
      <DropdownItem danger onClick={() => toast.info("Delete coming soon")}>
        <Trash2 size={13} />
        Delete agent
      </DropdownItem>
    </DropdownMenu>
  );
}

const columns: Column<Agent>[] = [
  {
    id: "name",
    header: "Agent",
    cell: (row) => (
      <div className="flex items-center gap-3">
        <div className="flex size-7 items-center justify-center rounded-[var(--radius-md)] bg-[var(--background-interactive)]">
          <Bot size={14} strokeWidth={1.8} className="text-[var(--foreground-muted)]" />
        </div>
        <div>
          <div className="flex items-center gap-2">
            <span className="text-[13px] font-medium text-[var(--foreground)]">
              {row.name}
            </span>
            {row.isAutonomous && (
              <Badge variant="accent">autonomous</Badge>
            )}
          </div>
          {row.description && (
            <p className="mt-0.5 max-w-xs truncate text-[11px] text-[var(--foreground-muted)]">
              {row.description}
            </p>
          )}
        </div>
      </div>
    ),
  },
  {
    id: "address",
    header: "Address",
    cell: (row) => (
      <span className="font-mono text-[12px]">
        {row.address.slice(0, 8)}...{row.address.slice(-4)}
      </span>
    ),
  },
  {
    id: "services",
    header: "Services",
    numeric: true,
    cell: (row) => {
      const count = row.services?.length ?? 0;
      const active = row.services?.filter((s) => s.active).length ?? 0;
      return (
        <div className="flex items-center gap-1.5">
          <span>{count}</span>
          {count > 0 && active < count && (
            <span className="text-[11px] text-[var(--foreground-muted)]">
              ({active} active)
            </span>
          )}
        </div>
      );
    },
  },
  {
    id: "received",
    header: "Received",
    numeric: true,
    cell: (row) => formatCurrency(row.stats.totalReceived),
  },
  {
    id: "txns",
    header: "Txns",
    numeric: true,
    cell: (row) => row.stats.transactionCount,
  },
  {
    id: "success",
    header: "Success",
    numeric: true,
    cell: (row) => {
      const rate = row.stats.successRate;
      return (
        <span
          className={
            rate >= 0.95
              ? "text-[var(--color-success)]"
              : rate >= 0.8
                ? "text-[var(--color-warning)]"
                : "text-[var(--color-danger)]"
          }
        >
          {(rate * 100).toFixed(1)}%
        </span>
      );
    },
  },
  {
    id: "created",
    header: "Registered",
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
    cell: (row) => <AgentActions agent={row} />,
  },
];

export function AgentsPage() {
  const agents = useAgents();

  return (
    <div className="min-h-screen">
      <header className="flex items-center justify-between border-b border-[var(--border)] px-8 py-5">
        <div>
          <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Agents</h1>
          <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
            Registered agents in the network
          </p>
        </div>
        <Button variant="primary" size="sm">
          <Plus size={14} />
          Register Agent
        </Button>
      </header>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={agents.data?.agents ?? []}
          isLoading={agents.isLoading}
          keyExtractor={(row) => row.address}
          emptyTitle="No agents registered"
          emptyDescription="Register your first agent to start using the network."
        />
      </div>
    </div>
  );
}
