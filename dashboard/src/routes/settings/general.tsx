import { useState } from "react";
import { Globe, Webhook, CreditCard, Shield, Settings, Plus, Trash2, Copy, Check, AlertTriangle, Loader2 } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useWebhooks, useCreateWebhook, useDeleteWebhook } from "@/hooks/api/use-webhooks";
import { useOverview } from "@/hooks/api/use-dashboard";
import { api, getTenantId } from "@/lib/api-client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { relativeTime, copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";

const WEBHOOK_EVENT_OPTIONS = [
  "payment.received",
  "payment.sent",
  "session_key.used",
  "session_key.created",
  "session_key.revoked",
  "session_key.budget_warning",
  "session_key.expiring",
  "balance.deposit",
  "balance.withdraw",
  "gateway.session.created",
  "gateway.session.closed",
  "gateway.proxy.success",
  "gateway.settlement.failed",
  "escrow.created",
  "escrow.delivered",
  "escrow.released",
  "escrow.refunded",
  "escrow.disputed",
  "stream.opened",
  "stream.closed",
  "stream.settled",
];

export function SettingsPage() {
  const overview = useOverview();
  const queryClient = useQueryClient();
  const tenantId = getTenantId();
  const tenant = overview.data?.tenant;
  const billing = overview.data?.billing;

  const [tenantName, setTenantName] = useState("");
  const [nameInitialized, setNameInitialized] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (tenant?.name && !nameInitialized) {
    setTenantName(tenant.name);
    setNameInitialized(true);
  }

  const saveMutation = useMutation({
    mutationFn: (name: string) =>
      api.patch(`/tenants/${tenantId}`, { name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "overview"] });
      toast.success("Settings saved");
    },
    onError: () => toast.error("Failed to save settings"),
  });

  const takeRate = billing?.takeRateBps ?? 0;
  const takeRatePct = (takeRate / 100).toFixed(1);

  return (
    <div className="min-h-screen">
      <PageHeader icon={Settings} title="Settings" description="Manage tenant configuration" />

      <div className="mx-auto max-w-2xl px-4 md:px-8 py-8">
        {/* General section */}
        <SettingsSection
          icon={Globe}
          title="General"
          description="Basic tenant information and preferences."
        >
          <div className="flex items-start justify-between gap-8">
            <div className="flex-1">
              <Input
                id="tenant-name"
                label="Tenant Name"
                value={tenantName}
                onChange={(e) => setTenantName(e.target.value)}
              />
            </div>
            <Button
              variant="primary"
              size="sm"
              className="mt-6"
              disabled={saveMutation.isPending || tenantName === tenant?.name}
              onClick={() => saveMutation.mutate(tenantName)}
            >
              {saveMutation.isPending ? (
                <Loader2 size={14} className="animate-spin" />
              ) : (
                "Save"
              )}
            </Button>
          </div>
          <SettingsRow label="Plan" description="Current subscription tier">
            <Badge variant="accent">{tenant?.plan ?? "—"}</Badge>
          </SettingsRow>
          <SettingsRow label="Take Rate" description="Fee applied to settled transactions">
            <span className="tabular-nums text-sm text-muted-foreground">
              {takeRate} bps ({takeRatePct}%)
            </span>
          </SettingsRow>
          <SettingsRow label="Tenant ID" description="Use this in API calls">
            <code className="rounded-sm bg-accent px-2 py-1 font-mono text-xs text-muted-foreground">
              {tenantId}
            </code>
          </SettingsRow>
        </SettingsSection>

        <Divider />

        {/* Webhooks section */}
        <WebhooksSection />

        <Divider />

        {/* Billing section */}
        <SettingsSection
          icon={CreditCard}
          title="Billing"
          description="Manage your subscription and payment method."
        >
          <SettingsRow label="Current Plan" description="Your active subscription">
            <Badge variant="accent">{tenant?.plan ?? "—"}</Badge>
          </SettingsRow>
          <SettingsRow label="Payment Method" description="Card on file">
            <span className="text-sm text-muted-foreground">
              Not configured
            </span>
          </SettingsRow>
          <p className="text-xs text-muted-foreground">
            Billing integration is not yet available. Contact support to manage your subscription.
          </p>
        </SettingsSection>

        <Divider />

        {/* Security */}
        <SettingsSection
          icon={Shield}
          title="Security"
          description="Authentication and access control settings."
        >
          <SettingsRow label="CORS Origins" description="Allowed origins for browser API calls">
            <code className="rounded-sm bg-accent px-2 py-1 font-mono text-xs text-muted-foreground">
              *
            </code>
          </SettingsRow>
          <SettingsRow
            label="Rate Limit"
            description="Max requests per minute per API key"
          >
            <span className="tabular-nums text-sm text-muted-foreground">
              1,000 req/min
            </span>
          </SettingsRow>
        </SettingsSection>

        <Divider />

        {/* Danger zone */}
        <section>
          <h2 className="text-sm font-medium text-destructive">Danger Zone</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Irreversible actions. Proceed with caution.
          </p>
          <div className="mt-4 rounded-lg border border-destructive/30 bg-destructive/5 p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium text-foreground">
                  Delete tenant
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Permanently delete this tenant and all associated data.
                </p>
              </div>
              <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>
                Delete Tenant
              </Button>
            </div>
          </div>
        </section>
      </div>

      {/* Delete Tenant Confirmation */}
      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Tenant</DialogTitle>
            <DialogDescription>
              This action cannot be undone. All agents, sessions, escrows, and API keys will be permanently deleted.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="rounded-md border border-destructive/20 bg-destructive/5 p-3 text-sm text-muted-foreground">
              Tenant deletion is not available from the dashboard. Please contact support.
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function WebhooksSection() {
  const webhooks = useWebhooks();
  const createMutation = useCreateWebhook();
  const deleteMutation = useDeleteWebhook();

  const [createOpen, setCreateOpen] = useState(false);
  const [url, setUrl] = useState("");
  const [selectedEvents, setSelectedEvents] = useState<Set<string>>(new Set());
  const [newSecret, setNewSecret] = useState<{ url: string; secret: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const resetForm = () => {
    setCreateOpen(false);
    setUrl("");
    setSelectedEvents(new Set());
  };

  const handleCreate = () => {
    createMutation.mutate(
      { url, events: Array.from(selectedEvents) },
      {
        onSuccess: (data) => {
          setNewSecret({ url: data.webhook.url, secret: data.secret });
          resetForm();
          toast.success("Webhook created");
        },
        onError: () => toast.error("Failed to create webhook"),
      }
    );
  };

  const handleDelete = (id: string) => {
    deleteMutation.mutate(id, {
      onSuccess: () => {
        setConfirmDelete(null);
        toast.success("Webhook deleted");
      },
      onError: () => toast.error("Failed to delete webhook"),
    });
  };

  const toggleEvent = (event: string) => {
    setSelectedEvents((prev) => {
      const next = new Set(prev);
      if (next.has(event)) next.delete(event);
      else next.add(event);
      return next;
    });
  };

  const hookList = webhooks.data?.webhooks ?? [];

  return (
    <>
      <SettingsSection
        icon={Webhook}
        title="Webhooks"
        description="Get notified when events happen in your account."
      >
        {/* Secret reveal banner */}
        {newSecret && (
          <div className="rounded-lg border border-warning bg-warning/10 p-4">
            <div className="flex items-start gap-3">
              <AlertTriangle size={16} className="mt-0.5 shrink-0 text-warning" />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-warning">
                  Webhook signing secret — save it now
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  For {newSecret.url}
                </p>
                <div className="mt-2 flex items-center gap-2">
                  <code className="flex-1 overflow-x-auto rounded-md border bg-background px-3 py-2 font-mono text-xs text-foreground">
                    {newSecret.secret}
                  </code>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={async () => {
                      await copyToClipboard(newSecret.secret);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 2000);
                    }}
                  >
                    {copied ? <Check size={13} className="text-success" /> : <Copy size={13} />}
                  </Button>
                </div>
                <Button variant="ghost" size="sm" className="mt-2" onClick={() => setNewSecret(null)}>
                  I've saved it — dismiss
                </Button>
              </div>
            </div>
          </div>
        )}

        {webhooks.isLoading ? (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
          </div>
        ) : hookList.length === 0 ? (
          <div className="rounded-lg border border-dashed bg-background px-4 py-6 text-center">
            <Webhook size={20} strokeWidth={1.5} className="mx-auto text-muted-foreground/50" />
            <p className="mt-2 text-sm text-muted-foreground">
              No webhooks configured
            </p>
            <p className="mt-1 text-xs text-muted-foreground/50">
              Receive real-time notifications for session events, settlements, and policy denials.
            </p>
            <Button variant="secondary" size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              Add Webhook Endpoint
            </Button>
          </div>
        ) : (
          <>
            <div className="flex flex-col gap-2">
              {hookList.map((wh) => (
                <div key={wh.id} className="flex items-center justify-between rounded-lg border bg-card px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-mono text-sm text-foreground">{wh.url}</span>
                      <Badge variant={wh.active ? "success" : "default"}>
                        {wh.active ? "active" : "inactive"}
                      </Badge>
                    </div>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {wh.events.map((e) => (
                        <Badge key={e} variant="default" className="text-[10px]">{e}</Badge>
                      ))}
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      Created {relativeTime(wh.createdAt)}
                    </p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setConfirmDelete(wh.id)}
                  >
                    <Trash2 size={14} className="text-destructive" />
                  </Button>
                </div>
              ))}
            </div>
            <Button variant="secondary" size="sm" onClick={() => setCreateOpen(true)}>
              <Plus size={14} />
              Add Webhook
            </Button>
          </>
        )}
      </SettingsSection>

      {/* Create Webhook Dialog */}
      <Dialog open={createOpen} onOpenChange={() => resetForm()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Webhook Endpoint</DialogTitle>
            <DialogDescription>
              A signing secret will be generated to verify payloads via HMAC-SHA256.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="flex flex-col gap-4">
              <Input
                id="wh-url"
                label="Endpoint URL"
                placeholder="https://example.com/webhooks"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                autoFocus
              />
              <div>
                <label className="text-sm font-medium text-foreground">Events</label>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Select the events to receive ({selectedEvents.size} selected)
                </p>
                <div className="mt-2 flex flex-wrap gap-1.5 rounded-md border bg-background p-3 max-h-48 overflow-y-auto">
                  {WEBHOOK_EVENT_OPTIONS.map((event) => (
                    <button
                      key={event}
                      type="button"
                      onClick={() => toggleEvent(event)}
                      className={`rounded-md border px-2 py-1 text-xs transition-colors ${
                        selectedEvents.has(event)
                          ? "border-accent-foreground/30 bg-accent text-accent-foreground"
                          : "border-transparent bg-muted text-muted-foreground hover:bg-accent/50"
                      }`}
                    >
                      {event}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={resetForm}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!url || selectedEvents.size === 0 || createMutation.isPending}
              onClick={handleCreate}
            >
              {createMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Creating...
                </>
              ) : (
                "Create Webhook"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation */}
      <Dialog open={!!confirmDelete} onOpenChange={() => setConfirmDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Webhook</DialogTitle>
            <DialogDescription>
              This webhook will stop receiving events immediately.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              disabled={deleteMutation.isPending}
              onClick={() => confirmDelete && handleDelete(confirmDelete)}
            >
              {deleteMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Deleting...
                </>
              ) : (
                "Delete Webhook"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function SettingsSection({
  icon: Icon,
  title,
  description,
  children,
}: {
  icon: typeof Globe;
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <section>
      <div className="flex items-center gap-2">
        <Icon size={15} strokeWidth={1.8} className="text-muted-foreground" />
        <h2 className="text-sm font-medium text-foreground">{title}</h2>
      </div>
      <p className="mt-1 text-xs text-muted-foreground">{description}</p>
      <div className="mt-5 flex flex-col gap-5">{children}</div>
    </section>
  );
}

function SettingsRow({
  label,
  description,
  children,
}: {
  label: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-8">
      <div>
        <span className="text-sm font-medium text-foreground">{label}</span>
        <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

function Divider() {
  return <hr className="my-8 border-border" />;
}
