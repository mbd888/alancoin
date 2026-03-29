import { Bot, Plus, MoreHorizontal, Eye, Trash2, Zap } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { useAgents } from "@/hooks/api/use-agents";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import type { Agent } from "@/lib/types";

function AgentActions({ agent }: { agent: Agent }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button aria-label="Agent actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
          <MoreHorizontal size={15} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => toast.info(`Agent: ${agent.address}`)}>
          <Eye size={13} />
          View details
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => toast.info("Add service coming soon")}>
          <Zap size={13} />
          Add service
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem danger onClick={() => toast.info("Delete coming soon")}>
          <Trash2 size={13} />
          Delete agent
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const columns: Column<Agent>[] = [
  {
    id: "name",
    header: "Agent",
    cell: (row) => (
      <div className="flex items-center gap-3">
        <div className="flex size-7 items-center justify-center rounded-md bg-accent">
          <Bot size={14} strokeWidth={1.8} className="text-muted-foreground" />
        </div>
        <div>
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground">
              {row.name}
            </span>
            {row.isAutonomous && (
              <Badge variant="accent">autonomous</Badge>
            )}
          </div>
          {row.description && (
            <p className="mt-0.5 max-w-xs truncate text-xs text-muted-foreground">
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
      <span className="font-mono text-xs">
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
            <span className="text-xs text-muted-foreground">
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
              ? "text-success"
              : rate >= 0.8
                ? "text-warning"
                : "text-destructive"
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
      <span className="text-xs text-muted-foreground">
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
      <PageHeader
        icon={Bot}
        title="Agents"
        description="Registered agents in the network"
        actions={
          <Button variant="primary" size="sm">
            <Plus size={14} />
            Register Agent
          </Button>
        }
      />

      <div className="px-4 md:px-8 py-4">
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
