import { useState, useMemo } from "react";
import { Bot, Plus, MoreHorizontal, Eye, Trash2, Zap, Loader2 } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@/components/layouts/page-header";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { useAgents } from "@/hooks/api/use-agents";
import { api } from "@/lib/api-client";
import { formatCurrency, relativeTime } from "@/lib/utils";
import { toast } from "sonner";
import type { Agent } from "@/lib/types";

function AgentActions({
  onDelete,
  onAddService,
  onViewDetails,
}: {
  onDelete: () => void;
  onAddService: () => void;
  onViewDetails: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button aria-label="Agent actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
          <MoreHorizontal size={15} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={onViewDetails}>
          <Eye size={13} />
          View details
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onAddService}>
          <Zap size={13} />
          Add service
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem danger onClick={onDelete}>
          <Trash2 size={13} />
          Delete agent
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const columns = (
  onDelete: (agent: Agent) => void,
  onAddService: (agent: Agent) => void,
  onViewDetails: (agent: Agent) => void
): Column<Agent>[] => [
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
    cell: (row) => (
      <AgentActions
        onDelete={() => onDelete(row)}
        onAddService={() => onAddService(row)}
        onViewDetails={() => onViewDetails(row)}
      />
    ),
  },
];

export function AgentsPage() {
  const agents = useAgents();
  const queryClient = useQueryClient();

  const [confirmDelete, setConfirmDelete] = useState<Agent | null>(null);
  const [addServiceAgent, setAddServiceAgent] = useState<Agent | null>(null);
  const [viewAgent, setViewAgent] = useState<Agent | null>(null);
  const [registerOpen, setRegisterOpen] = useState(false);
  const [regAddress, setRegAddress] = useState("");
  const [regName, setRegName] = useState("");
  const [regDescription, setRegDescription] = useState("");
  const [regEndpoint, setRegEndpoint] = useState("");
  const [serviceName, setServiceName] = useState("");
  const [serviceType, setServiceType] = useState("");
  const [servicePrice, setServicePrice] = useState("");
  const [serviceEndpoint, setServiceEndpoint] = useState("");
  const [serviceDescription, setServiceDescription] = useState("");

  const agentColumns = useMemo(() => columns(setConfirmDelete, setAddServiceAgent, setViewAgent), []);

  const deleteMutation = useMutation({
    mutationFn: (address: string) => api.delete(`/agents/${address}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
      setConfirmDelete(null);
      toast.success("Agent deleted");
    },
    onError: () => toast.error("Failed to delete agent"),
  });

  const registerMutation = useMutation({
    mutationFn: (body: { address: string; name: string; description?: string; endpoint?: string }) =>
      api.post("/agents", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
      setRegisterOpen(false);
      setRegAddress("");
      setRegName("");
      setRegDescription("");
      setRegEndpoint("");
      toast.success("Agent registered");
    },
    onError: () => toast.error("Failed to register agent"),
  });

  const addServiceMutation = useMutation({
    mutationFn: ({ address, body }: { address: string; body: Record<string, string> }) =>
      api.post(`/agents/${address}/services`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
      resetServiceForm();
      toast.success("Service added");
    },
    onError: () => toast.error("Failed to add service"),
  });

  const resetServiceForm = () => {
    setAddServiceAgent(null);
    setServiceName("");
    setServiceType("");
    setServicePrice("");
    setServiceEndpoint("");
    setServiceDescription("");
  };

  const handleAddService = () => {
    if (!addServiceAgent) return;
    addServiceMutation.mutate({
      address: addServiceAgent.address,
      body: {
        name: serviceName,
        type: serviceType,
        price: servicePrice,
        endpoint: serviceEndpoint,
        description: serviceDescription,
      },
    });
  };

  const canSubmitService = serviceName && serviceType && servicePrice && serviceEndpoint;

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={Bot}
        title="Agents"
        description="Registered agents in the network"
        actions={
          <Button variant="primary" size="sm" onClick={() => setRegisterOpen(true)}>
            <Plus size={14} />
            Register Agent
          </Button>
        }
      />

      <div className="px-4 md:px-8 py-4">
        <DataTable
          columns={agentColumns}
          data={agents.data?.agents ?? []}
          isLoading={agents.isLoading}
          keyExtractor={(row) => row.address}
          emptyTitle="No agents registered"
          emptyDescription="Register your first agent to start using the network."
        />
      </div>

      {/* Delete Confirmation */}
      <Dialog open={!!confirmDelete} onOpenChange={() => setConfirmDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Agent</DialogTitle>
            <DialogDescription>
              This will permanently remove the agent and all associated services. This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          {confirmDelete && (
            <DialogBody>
              <div className="rounded-md border bg-background p-3">
                <p className="text-sm font-medium text-foreground">{confirmDelete.name}</p>
                <p className="mt-1 font-mono text-xs text-muted-foreground">{confirmDelete.address}</p>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              disabled={deleteMutation.isPending}
              onClick={() => confirmDelete && deleteMutation.mutate(confirmDelete.address)}
            >
              {deleteMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Deleting...
                </>
              ) : (
                "Delete Agent"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Service Dialog */}
      <Dialog open={!!addServiceAgent} onOpenChange={() => resetServiceForm()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Service</DialogTitle>
            <DialogDescription>
              Register a new service for {addServiceAgent?.name ?? "this agent"}.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="flex flex-col gap-4">
              <Input
                id="svc-name"
                label="Service name"
                placeholder="e.g. GPT-4 Inference"
                value={serviceName}
                onChange={(e) => setServiceName(e.target.value)}
                autoFocus
              />
              <Input
                id="svc-type"
                label="Type"
                placeholder="e.g. llm_inference, data_retrieval"
                value={serviceType}
                onChange={(e) => setServiceType(e.target.value)}
              />
              <Input
                id="svc-price"
                label="Price (USDC per request)"
                placeholder="e.g. 0.001"
                value={servicePrice}
                onChange={(e) => setServicePrice(e.target.value)}
              />
              <Input
                id="svc-endpoint"
                label="Endpoint URL"
                placeholder="https://..."
                value={serviceEndpoint}
                onChange={(e) => setServiceEndpoint(e.target.value)}
              />
              <Input
                id="svc-desc"
                label="Description (optional)"
                placeholder="Brief description of the service"
                value={serviceDescription}
                onChange={(e) => setServiceDescription(e.target.value)}
              />
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={resetServiceForm}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!canSubmitService || addServiceMutation.isPending}
              onClick={handleAddService}
            >
              {addServiceMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Adding...
                </>
              ) : (
                "Add Service"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Agent Details Dialog */}
      <Dialog open={!!viewAgent} onOpenChange={() => setViewAgent(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{viewAgent?.name ?? "Agent Details"}</DialogTitle>
            <DialogDescription>
              {viewAgent?.description || "No description provided."}
            </DialogDescription>
          </DialogHeader>
          {viewAgent && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <DetailRow label="Address">
                  <code className="font-mono text-xs">{viewAgent.address}</code>
                </DetailRow>
                <DetailRow label="Endpoint">
                  {viewAgent.endpoint ? (
                    <code className="font-mono text-xs">{viewAgent.endpoint}</code>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </DetailRow>
                <DetailRow label="Autonomous">
                  {viewAgent.isAutonomous ? "Yes" : "No"}
                </DetailRow>
                <DetailRow label="Registered">
                  {relativeTime(viewAgent.createdAt)}
                </DetailRow>
                <hr className="border-border" />
                <DetailRow label="Total Received">
                  {formatCurrency(viewAgent.stats.totalReceived)}
                </DetailRow>
                <DetailRow label="Total Sent">
                  {formatCurrency(viewAgent.stats.totalSent)}
                </DetailRow>
                <DetailRow label="Transactions">
                  {viewAgent.stats.transactionCount}
                </DetailRow>
                <DetailRow label="Success Rate">
                  {(viewAgent.stats.successRate * 100).toFixed(1)}%
                </DetailRow>
                {viewAgent.services?.length > 0 && (
                  <>
                    <hr className="border-border" />
                    <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
                      Services ({viewAgent.services.length})
                    </p>
                    {viewAgent.services.map((s) => (
                      <div key={s.id} className="flex items-center justify-between rounded-md border bg-background px-3 py-2">
                        <div>
                          <span className="text-sm font-medium text-foreground">{s.name}</span>
                          <span className="ml-2 text-xs text-muted-foreground">{s.type}</span>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="text-xs tabular-nums">{formatCurrency(s.price)}</span>
                          <Badge variant={s.active ? "success" : "default"}>
                            {s.active ? "active" : "inactive"}
                          </Badge>
                        </div>
                      </div>
                    ))}
                  </>
                )}
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewAgent(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Register Agent Dialog */}
      <Dialog open={registerOpen} onOpenChange={setRegisterOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Register Agent</DialogTitle>
            <DialogDescription>
              Register a new agent on the network with an Ethereum-style address.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="flex flex-col gap-4">
              <Input
                id="reg-addr"
                label="Agent Address"
                placeholder="0x..."
                value={regAddress}
                onChange={(e) => setRegAddress(e.target.value)}
                autoFocus
              />
              <Input
                id="reg-name"
                label="Name"
                placeholder="e.g. My GPT-4 Agent"
                value={regName}
                onChange={(e) => setRegName(e.target.value)}
              />
              <Input
                id="reg-endpoint"
                label="Endpoint URL (optional)"
                placeholder="https://..."
                value={regEndpoint}
                onChange={(e) => setRegEndpoint(e.target.value)}
              />
              <Input
                id="reg-desc"
                label="Description (optional)"
                placeholder="What does this agent do?"
                value={regDescription}
                onChange={(e) => setRegDescription(e.target.value)}
              />
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setRegisterOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!regAddress || !regName || registerMutation.isPending}
              onClick={() =>
                registerMutation.mutate({
                  address: regAddress,
                  name: regName,
                  ...(regDescription && { description: regDescription }),
                  ...(regEndpoint && { endpoint: regEndpoint }),
                })
              }
            >
              {registerMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Registering...
                </>
              ) : (
                "Register Agent"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      <div className="text-right text-sm text-foreground">{children}</div>
    </div>
  );
}
