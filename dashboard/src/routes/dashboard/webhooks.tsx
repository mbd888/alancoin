import { useState, useCallback } from "react";
import {
  Bell,
  Plus,
  Trash2,
  Copy,
  Check,
  AlertTriangle,
  MoreHorizontal,
  Loader2,
  Eye,
  Globe,
} from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogBody,
  DialogFooter,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { useWebhooks, useCreateWebhook, useDeleteWebhook } from "@/hooks/api/use-webhooks";
import { relativeTime, copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import type { Webhook } from "@/lib/types";

const AVAILABLE_EVENTS = [
  "settlement.completed",
  "settlement.failed",
  "escrow.created",
  "escrow.delivered",
  "escrow.disputed",
  "escrow.released",
  "session.created",
  "session.expired",
  "tier.upgraded",
  "tier.downgraded",
  "stream.opened",
  "stream.closed",
  "alert.triggered",
];

export function WebhooksPage() {
  const webhooks = useWebhooks();
  const createMutation = useCreateWebhook();
  const deleteMutation = useDeleteWebhook();

  const [createOpen, setCreateOpen] = useState(false);
  const [createUrl, setCreateUrl] = useState("");
  const [selectedEvents, setSelectedEvents] = useState<Set<string>>(new Set());
  const [newSecret, setNewSecret] = useState<string | null>(null);
  const [secretCopied, setSecretCopied] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [viewWebhook, setViewWebhook] = useState<Webhook | null>(null);

  const toggleEvent = (event: string) => {
    setSelectedEvents((prev) => {
      const next = new Set(prev);
      if (next.has(event)) next.delete(event);
      else next.add(event);
      return next;
    });
  };

  const handleCreate = () => {
    if (!createUrl || selectedEvents.size === 0) return;
    createMutation.mutate(
      { url: createUrl, events: Array.from(selectedEvents) },
      {
        onSuccess: (data) => {
          setNewSecret(data.secret);
          setCreateOpen(false);
          setCreateUrl("");
          setSelectedEvents(new Set());
          toast.success("Webhook created");
        },
        onError: (err) => {
          const msg =
            err instanceof ApiError &&
            typeof err.body === "object" &&
            err.body !== null &&
            "message" in err.body
              ? (err.body as { message: string }).message
              : "Failed to create webhook";
          toast.error(msg);
        },
      }
    );
  };

  const handleDelete = (id: string) => {
    deleteMutation.mutate(id, {
      onSuccess: () => {
        setConfirmDelete(null);
        toast.success("Webhook deleted");
      },
      onError: () => {
        setConfirmDelete(null);
        toast.error("Failed to delete webhook");
      },
    });
  };

  const handleCopySecret = useCallback(async () => {
    if (!newSecret) return;
    await copyToClipboard(newSecret);
    setSecretCopied(true);
    toast.success("Copied to clipboard");
    setTimeout(() => setSecretCopied(false), 2000);
  }, [newSecret]);

  const webhookList = webhooks.data?.webhooks ?? [];

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={Bell}
        title="Webhooks"
        description="Receive event notifications at your endpoints"
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus size={14} />
            Add Webhook
          </Button>
        }
      />

      {/* Secret banner */}
      {newSecret && (
        <div className="mx-4 mt-6 md:mx-8 rounded-lg border border-warning bg-warning/10 p-5">
          <div className="flex items-start gap-3">
            <AlertTriangle size={16} className="mt-0.5 shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-warning">
                Save your webhook signing secret — it won't be shown again
              </p>
              <div className="mt-3 flex items-center gap-2">
                <code className="block flex-1 overflow-x-auto rounded-md border bg-background px-3 py-2 font-mono text-xs tabular-nums text-foreground">
                  {newSecret}
                </code>
                <Button variant="secondary" size="sm" onClick={handleCopySecret}>
                  {secretCopied ? (
                    <Check size={13} className="text-success" />
                  ) : (
                    <Copy size={13} />
                  )}
                </Button>
              </div>
              <div className="mt-3">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setNewSecret(null)}
                >
                  I've saved it — dismiss
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="px-4 md:px-8 py-6">
        {webhooks.isLoading ? (
          <div className="flex flex-col gap-4">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="flex items-center justify-between border-b py-4">
                <div className="flex items-center gap-4">
                  <Skeleton className="h-10 w-0.5" />
                  <div>
                    <Skeleton className="h-4 w-48" />
                    <Skeleton className="mt-2 h-3 w-32" />
                  </div>
                </div>
                <Skeleton className="h-8 w-8" />
              </div>
            ))}
          </div>
        ) : webhooks.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load webhooks
            <Button variant="ghost" size="sm" onClick={() => webhooks.refetch()}>
              Retry
            </Button>
          </div>
        ) : webhookList.length === 0 ? (
          <EmptyState
            icon={Bell}
            title="No webhooks"
            description="Add a webhook to receive real-time event notifications."
            action={
              <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
                <Plus size={14} />
                Add Webhook
              </Button>
            }
          />
        ) : (
          <div className="flex flex-col">
            {webhookList.map((wh) => (
              <WebhookRow
                key={wh.id}
                webhook={wh}
                onView={() => setViewWebhook(wh)}
                onDelete={() => setConfirmDelete(wh.id)}
              />
            ))}
          </div>
        )}

        <div className="mt-8 rounded-lg border bg-card p-5">
          <h3 className="text-sm font-medium text-foreground">
            Verifying Signatures
          </h3>
          <pre className="mt-3 overflow-x-auto rounded-md border bg-background p-4 font-mono text-xs leading-relaxed text-muted-foreground">
{`const crypto = require('crypto');

function verify(payload, signature, secret) {
  const expected = crypto
    .createHmac('sha256', secret)
    .update(payload)
    .digest('hex');
  return crypto.timingSafeEqual(
    Buffer.from(signature),
    Buffer.from(expected)
  );
}`}
          </pre>
        </div>
      </div>

      {/* Create dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Webhook</DialogTitle>
            <DialogDescription>
              We'll send POST requests with a signed JSON payload.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <div className="flex flex-col gap-4">
              <Input
                id="webhook-url"
                label="Endpoint URL"
                type="url"
                placeholder="https://example.com/webhooks/alancoin"
                value={createUrl}
                onChange={(e) => setCreateUrl(e.target.value)}
                autoFocus
              />
              <div>
                <label className="mb-2 block text-xs font-medium text-foreground">
                  Events
                </label>
                <div className="flex flex-wrap gap-1.5">
                  {AVAILABLE_EVENTS.map((event) => (
                    <button
                      key={event}
                      type="button"
                      onClick={() => toggleEvent(event)}
                      className={`rounded-full px-2.5 py-1 text-xs font-medium transition-colors ${
                        selectedEvents.has(event)
                          ? "bg-accent text-accent-foreground"
                          : "bg-muted text-muted-foreground/60 hover:text-muted-foreground"
                      }`}
                    >
                      {event}
                    </button>
                  ))}
                </div>
                {selectedEvents.size === 0 && (
                  <p className="mt-1.5 text-xs text-muted-foreground/60">
                    Select at least one event
                  </p>
                )}
              </div>
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={handleCreate}
              disabled={
                createMutation.isPending ||
                !createUrl ||
                selectedEvents.size === 0
              }
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

      {/* View details dialog */}
      <Dialog open={!!viewWebhook} onOpenChange={() => setViewWebhook(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Webhook Details</DialogTitle>
            <DialogDescription>Configuration and subscribed events.</DialogDescription>
          </DialogHeader>
          {viewWebhook && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">ID</span>
                  <code className="text-right font-mono text-xs">{viewWebhook.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">URL</span>
                  <span className="text-right text-xs break-all">{viewWebhook.url}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={viewWebhook.active ? "success" : "default"}>
                    {viewWebhook.active ? "active" : "inactive"}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div>
                  <span className="text-xs text-muted-foreground">Subscribed Events</span>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {viewWebhook.events.map((event) => (
                      <Badge key={event} variant="accent">
                        {event}
                      </Badge>
                    ))}
                  </div>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewWebhook.createdAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewWebhook(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
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
              onClick={() => confirmDelete && handleDelete(confirmDelete)}
              disabled={deleteMutation.isPending}
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
    </div>
  );
}

function WebhookRow({
  webhook,
  onView,
  onDelete,
}: {
  webhook: Webhook;
  onView: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="flex items-center justify-between border-b py-4 first:pt-0 last:border-b-0">
      <div className="flex items-center gap-4">
        <div
          className="h-10 w-0.5 rounded-full"
          style={{
            backgroundColor: webhook.active
              ? "var(--color-success)"
              : "var(--color-gray-5)",
          }}
        />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Globe size={13} className="shrink-0 text-muted-foreground" />
            <span className="truncate text-sm font-medium text-foreground">
              {webhook.url}
            </span>
            {!webhook.active && <Badge variant="default">inactive</Badge>}
          </div>
          <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
            <span>{webhook.events.length} events</span>
            <span className="text-muted-foreground/50">&middot;</span>
            <span>Created {relativeTime(webhook.createdAt)}</span>
          </div>
        </div>
      </div>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            aria-label="Webhook actions"
            className="rounded-sm p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            <MoreHorizontal size={15} />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={onView}>
            <Eye size={13} />
            View details
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem danger onClick={onDelete}>
            <Trash2 size={13} />
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
