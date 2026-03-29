import { Command } from "cmdk";
import { useNavigate } from "@tanstack/react-router";
import {
  LayoutDashboard,
  Radio,
  Bot,
  Key,
  Settings,
  Plus,
  Moon,
  Sun,
  Copy,
  Rss,
  Lock,
  Wallet,
  GitBranch,
  Zap,
  Store,
  ShieldAlert,
  TrendingDown,
  Shield,
  Brain,
  Activity,
} from "lucide-react";
import { useUiStore } from "@/stores/ui-store";
import { useEffect } from "react";
import { toast } from "sonner";
import { copyToClipboard } from "@/lib/utils";

const PAGES = [
  { to: "/overview", label: "Overview", icon: LayoutDashboard },
  { to: "/sessions", label: "Sessions", icon: Radio },
  { to: "/agents", label: "Agents", icon: Bot },
  { to: "/live-feed", label: "Live Feed", icon: Rss },
  { to: "/escrow", label: "Escrow", icon: Lock },
  { to: "/budget", label: "Budget", icon: Wallet },
  { to: "/workflows", label: "Workflows", icon: GitBranch },
  { to: "/streams", label: "Streams", icon: Zap },
  { to: "/marketplace", label: "Marketplace", icon: Store },
  { to: "/alerts", label: "Alerts", icon: ShieldAlert },
  { to: "/chargeback", label: "Chargeback", icon: TrendingDown },
  { to: "/certificates", label: "Certificates", icon: Shield },
  { to: "/intelligence", label: "Intelligence", icon: Brain },
  { to: "/health", label: "System Health", icon: Activity },
  { to: "/api-keys", label: "API Keys", icon: Key },
  { to: "/settings", label: "Settings", icon: Settings },
];

const itemClass =
  "flex cursor-pointer items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors aria-selected:bg-accent aria-selected:text-accent-foreground";

const groupHeadingClass =
  "[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground";

export function CommandPalette() {
  const { commandPaletteOpen, setCommandPaletteOpen, theme, toggleTheme } = useUiStore();
  const navigate = useNavigate();

  useEffect(() => {
    if (!commandPaletteOpen) return;
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setCommandPaletteOpen(false);
    };
    document.addEventListener("keydown", handleEsc);
    return () => document.removeEventListener("keydown", handleEsc);
  }, [commandPaletteOpen, setCommandPaletteOpen]);

  if (!commandPaletteOpen) return null;

  const runAndClose = (fn: () => void) => {
    fn();
    setCommandPaletteOpen(false);
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]"
      onClick={(e) => {
        if (e.target === e.currentTarget) setCommandPaletteOpen(false);
      }}
    >
      <div className="fixed inset-0 bg-black/60" />

      <Command
        className="relative w-full max-w-lg rounded-xl border bg-popover shadow-2xl"
        label="Command palette"
      >
        <Command.Input
          placeholder="Search pages, actions..."
          className="w-full border-b bg-transparent px-4 py-3 text-sm text-foreground placeholder:text-muted-foreground outline-none"
          autoFocus
        />
        <Command.List className="max-h-80 overflow-y-auto p-2">
          <Command.Empty className="px-4 py-8 text-center text-sm text-muted-foreground">
            No results found.
          </Command.Empty>

          {/* Navigation */}
          <Command.Group heading="Navigate" className={groupHeadingClass}>
            {PAGES.map((page) => {
              const Icon = page.icon;
              return (
                <Command.Item
                  key={page.to}
                  value={`navigate ${page.label}`}
                  onSelect={() => runAndClose(() => navigate({ to: page.to }))}
                  className={itemClass}
                >
                  <Icon size={15} strokeWidth={1.8} />
                  <span>{page.label}</span>
                </Command.Item>
              );
            })}
          </Command.Group>

          {/* Actions */}
          <Command.Group heading="Actions" className={groupHeadingClass}>
            <Command.Item
              value="create api key"
              onSelect={() => runAndClose(() => navigate({ to: "/api-keys" }))}
              className={itemClass}
            >
              <Plus size={15} strokeWidth={1.8} />
              <span>Create API Key</span>
            </Command.Item>
            <Command.Item
              value="register agent"
              onSelect={() => runAndClose(() => navigate({ to: "/agents" }))}
              className={itemClass}
            >
              <Bot size={15} strokeWidth={1.8} />
              <span>Register Agent</span>
            </Command.Item>
            <Command.Item
              value="toggle theme dark light mode"
              onSelect={() => runAndClose(toggleTheme)}
              className={itemClass}
            >
              {theme === "dark" ? (
                <Sun size={15} strokeWidth={1.8} />
              ) : (
                <Moon size={15} strokeWidth={1.8} />
              )}
              <span>{theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}</span>
            </Command.Item>
            <Command.Item
              value="copy api base url"
              onSelect={() =>
                runAndClose(async () => {
                  await copyToClipboard("https://api.alancoin.dev/v1");
                  toast.success("API base URL copied");
                })
              }
              className={itemClass}
            >
              <Copy size={15} strokeWidth={1.8} />
              <span>Copy API base URL</span>
            </Command.Item>
          </Command.Group>
        </Command.List>

        <div className="flex items-center gap-4 border-t px-4 py-2 text-xs text-muted-foreground">
          <span>
            <kbd className="font-mono">↑↓</kbd> navigate
          </span>
          <span>
            <kbd className="font-mono">↵</kbd> select
          </span>
          <span>
            <kbd className="font-mono">esc</kbd> close
          </span>
        </div>
      </Command>
    </div>
  );
}
