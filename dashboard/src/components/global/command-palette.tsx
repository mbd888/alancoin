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
} from "lucide-react";
import { useUiStore } from "@/stores/ui-store";
import { useEffect } from "react";
import { toast } from "sonner";
import { copyToClipboard } from "@/lib/utils";

const PAGES = [
  { to: "/overview", label: "Overview", icon: LayoutDashboard, group: "Navigate" },
  { to: "/sessions", label: "Sessions", icon: Radio, group: "Navigate" },
  { to: "/agents", label: "Agents", icon: Bot, group: "Navigate" },
  { to: "/api-keys", label: "API Keys", icon: Key, group: "Navigate" },
  { to: "/settings", label: "Settings", icon: Settings, group: "Navigate" },
];

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
        className="relative w-full max-w-lg rounded-[var(--radius-xl)] border border-[var(--border)] bg-[var(--background-elevated)] shadow-2xl"
        label="Command palette"
      >
        <Command.Input
          placeholder="Search pages, actions..."
          className="w-full border-b border-[var(--border)] bg-transparent px-4 py-3 text-sm text-[var(--foreground)] placeholder:text-[var(--foreground-disabled)] outline-none"
          autoFocus
        />
        <Command.List className="max-h-80 overflow-y-auto p-2">
          <Command.Empty className="px-4 py-8 text-center text-[13px] text-[var(--foreground-muted)]">
            No results found.
          </Command.Empty>

          {/* Navigation */}
          <Command.Group
            heading="Navigate"
            className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-[var(--foreground-muted)]"
          >
            {PAGES.map((page) => {
              const Icon = page.icon;
              return (
                <Command.Item
                  key={page.to}
                  value={`navigate ${page.label}`}
                  onSelect={() => runAndClose(() => navigate({ to: page.to }))}
                  className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-md)] px-3 py-2 text-[13px] text-[var(--foreground-secondary)] transition-[background-color] duration-100 aria-selected:bg-[var(--background-interactive)] aria-selected:text-[var(--foreground)]"
                >
                  <Icon size={15} strokeWidth={1.8} />
                  <span>{page.label}</span>
                </Command.Item>
              );
            })}
          </Command.Group>

          {/* Actions */}
          <Command.Group
            heading="Actions"
            className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-[var(--foreground-muted)]"
          >
            <Command.Item
              value="create api key"
              onSelect={() => runAndClose(() => navigate({ to: "/api-keys" }))}
              className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-md)] px-3 py-2 text-[13px] text-[var(--foreground-secondary)] transition-[background-color] duration-100 aria-selected:bg-[var(--background-interactive)] aria-selected:text-[var(--foreground)]"
            >
              <Plus size={15} strokeWidth={1.8} />
              <span>Create API Key</span>
            </Command.Item>
            <Command.Item
              value="register agent"
              onSelect={() => runAndClose(() => navigate({ to: "/agents" }))}
              className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-md)] px-3 py-2 text-[13px] text-[var(--foreground-secondary)] transition-[background-color] duration-100 aria-selected:bg-[var(--background-interactive)] aria-selected:text-[var(--foreground)]"
            >
              <Bot size={15} strokeWidth={1.8} />
              <span>Register Agent</span>
            </Command.Item>
            <Command.Item
              value="toggle theme dark light mode"
              onSelect={() => runAndClose(toggleTheme)}
              className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-md)] px-3 py-2 text-[13px] text-[var(--foreground-secondary)] transition-[background-color] duration-100 aria-selected:bg-[var(--background-interactive)] aria-selected:text-[var(--foreground)]"
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
              className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-md)] px-3 py-2 text-[13px] text-[var(--foreground-secondary)] transition-[background-color] duration-100 aria-selected:bg-[var(--background-interactive)] aria-selected:text-[var(--foreground)]"
            >
              <Copy size={15} strokeWidth={1.8} />
              <span>Copy API base URL</span>
            </Command.Item>
          </Command.Group>
        </Command.List>

        <div className="flex items-center gap-4 border-t border-[var(--border)] px-4 py-2 text-[11px] text-[var(--foreground-disabled)]">
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
