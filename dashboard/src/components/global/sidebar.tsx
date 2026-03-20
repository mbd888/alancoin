import { Link, useMatchRoute } from "@tanstack/react-router";
import {
  LayoutDashboard,
  Radio,
  Bot,
  Key,
  Settings,
  ChevronsLeft,
  ChevronsRight,
  Moon,
  Sun,
  Command,
  ShieldAlert,
  TrendingDown,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useUiStore } from "@/stores/ui-store";

const NAV_ITEMS = [
  { to: "/overview", label: "Overview", icon: LayoutDashboard },
  { to: "/sessions", label: "Sessions", icon: Radio },
  { to: "/agents", label: "Agents", icon: Bot },
  { divider: true as const },
  { to: "/alerts", label: "Alerts", icon: ShieldAlert },
  { to: "/chargeback", label: "Chargeback", icon: TrendingDown },
  { divider: true as const },
  { to: "/api-keys", label: "API Keys", icon: Key },
  { divider: true as const },
  { to: "/settings", label: "Settings", icon: Settings },
] as const;

type NavItem =
  | { to: string; label: string; icon: typeof LayoutDashboard; divider?: never }
  | { divider: true; to?: never; label?: never; icon?: never };

export function Sidebar() {
  const { sidebarCollapsed, toggleSidebar, theme, toggleTheme, setCommandPaletteOpen } =
    useUiStore();
  const matchRoute = useMatchRoute();

  return (
    <aside
      className={cn(
        "fixed inset-y-0 left-0 z-30 flex flex-col border-r",
        "bg-[var(--sidebar-bg)] border-[var(--sidebar-border)]",
      )}
      style={{
        width: sidebarCollapsed ? 56 : 240,
        transition: "width 200ms cubic-bezier(0.16, 1, 0.3, 1)",
      }}
    >
      {/* Logo */}
      <div className="flex h-14 items-center gap-2 border-b border-[var(--sidebar-border)] px-4">
        <div className="flex size-7 items-center justify-center rounded-[var(--radius-md)] bg-[var(--color-accent-5)]">
          <span className="text-xs font-bold text-white">A</span>
        </div>
        {!sidebarCollapsed && (
          <span className="text-sm font-semibold text-[var(--foreground)]">
            Alancoin
          </span>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto px-2 py-3">
        <ul className="flex flex-col gap-0.5">
          {(NAV_ITEMS as readonly NavItem[]).map((item, i) => {
            if (item.divider) {
              return (
                <li key={`d-${i}`} className="my-2 border-t border-[var(--border-subtle)]" />
              );
            }
            const isActive = matchRoute({ to: item.to, fuzzy: true });
            const Icon = item.icon;
            return (
              <li key={item.to}>
                <Link
                  to={item.to}
                  className={cn(
                    "flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-1.5",
                    "text-[13px] font-medium",
                    "transition-[background-color,color] duration-150",
                    isActive
                      ? "bg-[var(--background-interactive)] text-[var(--foreground)]"
                      : "text-[var(--foreground-muted)] hover:bg-[var(--background-interactive)] hover:text-[var(--foreground-secondary)]"
                  )}
                  title={sidebarCollapsed ? item.label : undefined}
                >
                  <Icon size={16} strokeWidth={1.8} className="shrink-0" />
                  <span
                    className="overflow-hidden whitespace-nowrap"
                    style={{
                      width: sidebarCollapsed ? 0 : "auto",
                      opacity: sidebarCollapsed ? 0 : 1,
                      transition: "opacity 150ms ease",
                    }}
                  >
                    {item.label}
                  </span>
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>

      {/* Footer actions */}
      <div className="flex flex-col gap-1 border-t border-[var(--sidebar-border)] px-2 py-3">
        {/* Command palette trigger */}
        <button
          onClick={() => setCommandPaletteOpen(true)}
          className={cn(
            "flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-1.5",
            "text-[12px] text-[var(--foreground-muted)]",
            "transition-[background-color] duration-150",
            "hover:bg-[var(--background-interactive)]"
          )}
        >
          <Command size={14} strokeWidth={1.8} className="shrink-0" />
          {!sidebarCollapsed && (
            <span className="flex items-center gap-2">
              Search
              <kbd className="rounded border border-[var(--border)] bg-[var(--background)] px-1 py-0.5 text-[10px] font-mono">
                ⌘K
              </kbd>
            </span>
          )}
        </button>

        {/* Theme toggle */}
        <button
          onClick={toggleTheme}
          className={cn(
            "flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-1.5",
            "text-[12px] text-[var(--foreground-muted)]",
            "transition-[background-color] duration-150",
            "hover:bg-[var(--background-interactive)]"
          )}
        >
          {theme === "dark" ? (
            <Sun size={14} strokeWidth={1.8} className="shrink-0" />
          ) : (
            <Moon size={14} strokeWidth={1.8} className="shrink-0" />
          )}
          {!sidebarCollapsed && <span>{theme === "dark" ? "Light mode" : "Dark mode"}</span>}
        </button>

        {/* Collapse toggle */}
        <button
          onClick={toggleSidebar}
          className={cn(
            "flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-1.5",
            "text-[12px] text-[var(--foreground-muted)]",
            "transition-[background-color] duration-150",
            "hover:bg-[var(--background-interactive)]"
          )}
        >
          {sidebarCollapsed ? (
            <ChevronsRight size={14} strokeWidth={1.8} className="shrink-0" />
          ) : (
            <ChevronsLeft size={14} strokeWidth={1.8} className="shrink-0" />
          )}
          {!sidebarCollapsed && <span>Collapse</span>}
        </button>
      </div>
    </aside>
  );
}
