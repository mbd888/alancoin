import { useState, useCallback } from "react";
import { Plus, Copy, Check, AlertTriangle, MoreHorizontal, Trash2, RotateCcw, Key } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { SecretDisplay } from "@/components/domain/secret-display";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { relativeTime, copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";

interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt: string | null;
  status: "active" | "revoked";
  environment: "live" | "test";
}

const INITIAL_KEYS: ApiKey[] = [
  {
    id: "key_1",
    name: "Production Key",
    prefix: "ak_live_Tf2xR8mN...9k4a",
    createdAt: "2026-03-10T08:00:00Z",
    lastUsedAt: "2026-03-17T14:30:00Z",
    status: "active",
    environment: "live",
  },
  {
    id: "key_2",
    name: "Development Key",
    prefix: "ak_test_Qm8rP5yL...3j7b",
    createdAt: "2026-03-05T12:00:00Z",
    lastUsedAt: "2026-03-16T09:15:00Z",
    status: "active",
    environment: "test",
  },
];

export function ApiKeysPage() {
  const [keys, setKeys] = useState(INITIAL_KEYS);
  const [createOpen, setCreateOpen] = useState(false);
  const [newKeyDisplay, setNewKeyDisplay] = useState<{ name: string; key: string } | null>(null);
  const [copied, setCopied] = useState(false);

  // Create key form state
  const [createName, setCreateName] = useState("");
  const [createEnv, setCreateEnv] = useState("test");

  const handleCreate = () => {
    // Simulate key generation
    const prefix = createEnv === "live" ? "ak_live_" : "ak_test_";
    const randomPart = Array.from(crypto.getRandomValues(new Uint8Array(24)))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const fullKey = `${prefix}${randomPart}`;
    const masked = `${prefix}${randomPart.slice(0, 4)}...${randomPart.slice(-4)}`;

    const newKey: ApiKey = {
      id: `key_${Date.now()}`,
      name: createName || "Untitled Key",
      prefix: masked,
      createdAt: new Date().toISOString(),
      lastUsedAt: null,
      status: "active",
      environment: createEnv as "live" | "test",
    };

    setKeys((prev) => [newKey, ...prev]);
    setNewKeyDisplay({ name: newKey.name, key: fullKey });
    setCreateOpen(false);
    setCreateName("");
    setCreateEnv("test");
    toast.success("API key created");
  };

  const handleRevoke = (id: string) => {
    setKeys((prev) =>
      prev.map((k) => (k.id === id ? { ...k, status: "revoked" as const } : k))
    );
    toast.success("Key revoked");
  };

  const handleCopyNewKey = useCallback(async () => {
    if (!newKeyDisplay) return;
    await copyToClipboard(newKeyDisplay.key);
    setCopied(true);
    toast.success("Copied to clipboard");
    setTimeout(() => setCopied(false), 2000);
  }, [newKeyDisplay]);

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

      {/* One-time key reveal banner */}
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

      {/* Key list */}
      <div className="px-4 md:px-8 py-6">
        <div className="flex flex-col">
          {keys.map((key) => (
            <div
              key={key.id}
              className="flex items-center justify-between border-b py-4 first:pt-0 last:border-b-0"
            >
              <div className="flex items-center gap-4">
                <div
                  className="h-10 w-0.5 rounded-full"
                  style={{
                    backgroundColor:
                      key.status === "revoked"
                        ? "var(--color-gray-5)"
                        : key.environment === "live"
                          ? "var(--color-success)"
                          : "var(--color-warning)",
                  }}
                />
                <div>
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-foreground">
                      {key.name}
                    </span>
                    <Badge variant={key.environment === "live" ? "success" : "warning"}>
                      {key.environment}
                    </Badge>
                    {key.status === "revoked" && <Badge variant="danger">revoked</Badge>}
                  </div>
                  <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                    <span>Created {relativeTime(key.createdAt)}</span>
                    {key.lastUsedAt && (
                      <>
                        <span className="text-muted-foreground/50">&middot;</span>
                        <span>Last used {relativeTime(key.lastUsedAt)}</span>
                      </>
                    )}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-2">
                <SecretDisplay value={key.prefix} />
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button aria-label="Key actions" className="rounded-sm p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                      <MoreHorizontal size={15} />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={() => toast.info("Rotation coming soon")}>
                      <RotateCcw size={13} />
                      Rotate key
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      danger
                      disabled={key.status === "revoked"}
                      onClick={() => handleRevoke(key.id)}
                    >
                      <Trash2 size={13} />
                      Revoke key
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            </div>
          ))}
        </div>

        {/* Quick start snippet */}
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
            <div className="flex flex-col gap-4">
              <Input
                id="key-name"
                label="Key name"
                placeholder="e.g. Production, CI/CD"
                value={createName}
                onChange={(e) => setCreateName(e.target.value)}
                autoFocus
              />
              <Select
                id="key-env"
                label="Environment"
                value={createEnv}
                onChange={(e) => setCreateEnv(e.target.value)}
                options={[
                  { value: "test", label: "Test — sandbox, no real charges" },
                  { value: "live", label: "Live — production traffic" },
                ]}
              />
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" onClick={handleCreate}>
              Create Key
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
