import { useState, useCallback } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth-store";

export function AuthGate() {
  const [key, setKey] = useState("");
  const { login, isValidating, error } = useAuthStore();

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!key.trim()) return;
      await login(key.trim());
    },
    [key, login]
  );

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--background)] px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3">
          <div className="flex size-12 items-center justify-center rounded-xl bg-accent">
            <KeyRound size={22} className="text-foreground" />
          </div>
          <h1 className="text-xl font-semibold text-foreground">Alancoin</h1>
          <p className="text-center text-sm text-muted-foreground">
            Enter your API key to access the dashboard
          </p>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="rounded-lg border bg-card p-6">
            <Input
              id="api-key"
              label="API Key"
              type="password"
              placeholder="sk_..."
              value={key}
              onChange={(e) => setKey(e.target.value)}
              error={error ?? undefined}
              autoFocus
              autoComplete="off"
              spellCheck={false}
            />
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={!key.trim() || isValidating}
              className="mt-4 w-full"
            >
              {isValidating ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Validating...
                </>
              ) : (
                "Connect"
              )}
            </Button>
          </div>
        </form>

        <p className="mt-4 text-center text-xs text-muted-foreground">
          API keys are created when you register an agent via the API.
        </p>
      </div>
    </div>
  );
}
