import { useState, useCallback } from "react";
import { Plus, Copy, Check, AlertTriangle, MoreHorizontal, Trash2, RotateCcw, Key, Loader2 } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/ui/empty-state";
import { useApiKeys, useCreateApiKey, useRevokeApiKey, useRotateApiKey } from "@/hooks/api/use-api-keys";
import { relativeTime, copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import type { AuthApiKey } from "@/lib/types";

export function ApiKeysPage() {
  const keys = useApiKeys();
  const createMutation = useCreateApiKey();
  const revokeMutation = useRevokeApiKey();
  const rotateMutation = useRotateApiKey();

  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [newKeyDisplay, setNewKeyDisplay] = useState<{ name: string; key: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);

  const handleCreate = () => {
    createMutation.mutate(createName || "Untitled Key", {
      onSuccess: (data) => {
        setNewKeyDisplay({ name: data.name, key: data.apiKey });
        setCreateOpen(false);
        setCreateName("");
        toast.success("API key created");
      },
      onError: () => toast.error("Failed to create key"),
    });
  };

  const handleRevoke = (id: string) => {
    revokeMutation.mutate(id, {
      onSuccess: () => {
        setConfirmRevoke(null);
        toast.success("Key revoked");
      },
      onError: (err) => {
        setConfirmRevoke(null);
        const msg = err instanceof ApiError && typeof err.body === "object" && err.body !== null && "message" in err.body
          ? (err.body as { message: string }).message
          : "Failed to revoke key";
        toast.error(msg);
      },
    });
  };

  const handleRotate = (id: string) => {
    rotateMutation.mutate(id, {
      onSuccess: (data) => {
        setNewKeyDisplay({ name: "Rotated key", key: data.apiKey });
        toast.success("Key rotated");
      },
      onError: () => toast.error("Failed to rotate key"),
    });
  };

  const handleCopyNewKey = useCallback(async () => {
    if (!newKeyDisplay) return;
    await copyToClipboard(newKeyDisplay.key);
    setCopied(true);
    toast.success("Copied to clipboard");
    setTimeout(() => setCopied(false), 2000);
  }, [newKeyDisplay]);

  const keyList = keys.data?.keys ?? [];

  return (
    <div className="min-h-screen">
      <PageHeader
        icon={Key}
        title="API Keys"
        description="Manage authentication keys for API access"
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus size={14} />
            Create Key
          </Button>
        }
      />

      {newKeyDisplay && (
        <div className="mx-4 mt-6 md:mx-8 rounded-lg border border-warning bg-warning/10 p-5">
          <div className="flex items-start gap-3">
            <AlertTriangle size={16} className="mt-0.5 shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-warning">
                Save your key for "{newKeyDisplay.name}" — it won't be shown again
              </p>
              <div className="mt-3 flex items-center gap-2">
                <code className="block flex-1 overflow-x-auto rounded-md border bg-background px-3 py-2 font-mono text-xs tabular-nums text-foreground">
                  {newKeyDisplay.key}
                </code>
                <Button variant="secondary" size="sm" onClick={handleCopyNewKey}>
                  {copied ? (
                    <Check size={13} className="text-success" />
                  ) : (
                    <Copy size={13} />
                  )}
                </Button>
              </div>
              <div className="mt-3">
                <Button variant="ghost" size="sm" onClick={() => setNewKeyDisplay(null)}>
                  I've saved it — dismiss
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="px-4 md:px-8 py-6">
        {keys.isLoading ? (
          <div className="flex flex-col gap-4">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="flex items-center justify-between border-b py-4">
                <div className="flex items-center gap-4">
                  <Skeleton className="h-10 w-0.5" />
                  <div>
                    <Skeleton className="h-4 w-32" />
                    <Skeleton className="mt-2 h-3 w-48" />
                  </div>
                </div>
                <Skeleton className="h-8 w-8" />
              </div>
            ))}
          </div>
        ) : keys.isError ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-6 text-center">
            <AlertTriangle size={20} className="mx-auto mb-2 text-destructive" />
            <p className="text-sm text-destructive">Failed to load API keys</p>
            <Button variant="ghost" size="sm" className="mt-2" onClick={() => keys.refetch()}>
              Retry
            </Button>
          </div>
        ) : keyList.length === 0 ? (
          <EmptyState
            icon={Key}
            title="No API keys"
            description="Create your first key to authenticate API requests."
            action={
              <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
                <Plus size={14} />
                Create Key
              </Button>
            }
          />
        ) : (
          <div className="flex flex-col">
            {keyList.map((key) => (
              <KeyRow
                key={key.id}
                apiKey={key}
                onRevoke={() => setConfirmRevoke(key.id)}
                onRotate={() => handleRotate(key.id)}
                isRotating={rotateMutation.isPending}
              />
            ))}
          </div>
        )}

        <div className="mt-8 rounded-lg border bg-card p-5">
          <h3 className="text-sm font-medium text-foreground">Quick Start</h3>
          <pre className="mt-3 overflow-x-auto rounded-md border bg-background p-4 font-mono text-xs leading-relaxed text-muted-foreground">
{`curl -H "Authorization: Bearer YOUR_API_KEY" \\
  https://api.alancoin.dev/v1/agents`}
          </pre>
        </div>
      </div>

      {/* Create Key Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create API Key</DialogTitle>
            <DialogDescription>
              The full key is shown once after creation.
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <Input
              id="key-name"
              label="Key name"
              placeholder="e.g. Production, CI/CD"
              value={createName}
              onChange={(e) => setCreateName(e.target.value)}
              autoFocus
            />
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={handleCreate}
              disabled={createMutation.isPending}
            >
              {createMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Creating...
                </>
              ) : (
                "Create Key"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Revoke Confirmation Dialog */}
      <Dialog open={!!confirmRevoke} onOpenChange={() => setConfirmRevoke(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke API Key</DialogTitle>
            <DialogDescription>
              This key will immediately stop working. Any services using it will lose access.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setConfirmRevoke(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={() => confirmRevoke && handleRevoke(confirmRevoke)}
              disabled={revokeMutation.isPending}
            >
              {revokeMutation.isPending ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Revoking...
                </>
              ) : (
                "Revoke Key"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function KeyRow({
  apiKey,
  onRevoke,
  onRotate,
  isRotating,
}: {
  apiKey: AuthApiKey;
  onRevoke: () => void;
  onRotate: () => void;
  isRotating: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-b py-4 first:pt-0 last:border-b-0">
      <div className="flex items-center gap-4">
        <div
          className="h-10 w-0.5 rounded-full"
          style={{
            backgroundColor: apiKey.revoked
              ? "var(--color-gray-5)"
              : "var(--color-success)",
          }}
        />
        <div>
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground">
              {apiKey.name}
            </span>
            {apiKey.revoked && <Badge variant="danger">revoked</Badge>}
          </div>
          <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
            <span>Created {relativeTime(apiKey.createdAt)}</span>
            {apiKey.lastUsed && (
              <>
                <span className="text-muted-foreground/50">&middot;</span>
                <span>Last used {relativeTime(apiKey.lastUsed)}</span>
              </>
            )}
          </div>
        </div>
      </div>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            aria-label="Key actions"
            className="rounded-sm p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            <MoreHorizontal size={15} />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={onRotate} disabled={apiKey.revoked || isRotating}>
            <RotateCcw size={13} />
            Rotate key
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem danger disabled={apiKey.revoked} onClick={onRevoke}>
            <Trash2 size={13} />
            Revoke key
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
