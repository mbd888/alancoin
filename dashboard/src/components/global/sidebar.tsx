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
  Shield,
  Brain,
  Activity,
  Rss,
  Lock,
  Wallet,
  GitBranch,
  Zap,
  Store,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useUiStore } from "@/stores/ui-store";
import { Separator } from "@/components/ui/separator";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

interface NavGroup {
  label: string;
  items: { to: string; label: string; icon: typeof LayoutDashboard }[];
}

const NAV_GROUPS: NavGroup[] = [
  {
    label: "Core",
    items: [
      { to: "/overview", label: "Overview", icon: LayoutDashboard },
      { to: "/sessions", label: "Sessions", icon: Radio },
      { to: "/agents", label: "Agents", icon: Bot },
      { to: "/live-feed", label: "Live Feed", icon: Rss },
    ],
  },
  {
    label: "Payments",
    items: [
      { to: "/escrow", label: "Escrow", icon: Lock },
      { to: "/budget", label: "Budget", icon: Wallet },
      { to: "/workflows", label: "Workflows", icon: GitBranch },
      { to: "/streams", label: "Streams", icon: Zap },
      { to: "/marketplace", label: "Marketplace", icon: Store },
    ],
  },
  {
    label: "Risk & Compliance",
    items: [
      { to: "/alerts", label: "Alerts", icon: ShieldAlert },
      { to: "/chargeback", label: "Chargeback", icon: TrendingDown },
      { to: "/certificates", label: "Certificates", icon: Shield },
      { to: "/intelligence", label: "Intelligence", icon: Brain },
      { to: "/health", label: "System Health", icon: Activity },
    ],
  },
  {
    label: "Access",
    items: [
      { to: "/api-keys", label: "API Keys", icon: Key },
    ],
  },
  {
    label: "Config",
    items: [
      { to: "/settings", label: "Settings", icon: Settings },
    ],
  },
];

export function SidebarContent({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const { toggleSidebar, theme, toggleTheme, setCommandPaletteOpen } =
    useUiStore();
  const matchRoute = useMatchRoute();

  return (
    <>
      {/* Logo */}
      <div className="flex h-14 items-center gap-2 border-b border-[var(--sidebar-border)] px-4">
        <img src="/alancoin-icon.png" alt="Alancoin" className="size-7 rounded-md" />
        {!collapsed && (
          <span className="text-sm font-semibold text-foreground">
            Alancoin
          </span>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto px-2 py-3">
        <div className="flex flex-col gap-4">
          {NAV_GROUPS.map((group, gi) => (
            <div key={group.label}>
              {gi > 0 && <Separator className="mb-3" />}
              {!collapsed && (
                <span className="mb-1 block px-3 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                  {group.label}
                </span>
              )}
              <ul className="flex flex-col gap-0.5">
                {group.items.map((item) => {
                  const isActive = matchRoute({ to: item.to, fuzzy: true });
                  const Icon = item.icon;

                  const link = (
                    <Link
                      to={item.to}
                      onClick={onNavigate}
                      className={cn(
                        "flex items-center gap-3 rounded-md px-3 py-1.5",
                        "text-sm font-medium",
                        "transition-colors",
                        isActive
                          ? "bg-accent text-accent-foreground"
                          : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                      )}
                    >
                      <Icon size={16} strokeWidth={1.8} className="shrink-0" />
                      <span
                        className="overflow-hidden whitespace-nowrap"
                        style={{
                          width: collapsed ? 0 : "auto",
                          opacity: collapsed ? 0 : 1,
                          transition: "opacity 150ms ease",
                        }}
                      >
                        {item.label}
                      </span>
                    </Link>
                  );

                  return (
                    <li key={item.to}>
                      {collapsed ? (
                        <Tooltip>
                          <TooltipTrigger asChild>{link}</TooltipTrigger>
                          <TooltipContent side="right">{item.label}</TooltipContent>
                        </Tooltip>
                      ) : (
                        link
                      )}
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
      </nav>

      {/* Footer actions */}
      <div className="flex flex-col gap-1 border-t border-[var(--sidebar-border)] px-2 py-3">
        {/* Command palette trigger */}
        <button
          aria-label="Search"
          onClick={() => setCommandPaletteOpen(true)}
          className={cn(
            "flex items-center gap-3 rounded-md px-3 py-1.5",
            "text-xs text-muted-foreground",
            "transition-colors",
            "hover:bg-accent hover:text-accent-foreground"
          )}
        >
          <Command size={14} strokeWidth={1.8} className="shrink-0" />
          {!collapsed && (
            <span className="flex items-center gap-2">
              Search
              <kbd className="rounded border bg-background px-1 py-0.5 text-[10px] font-mono">
                ⌘K
              </kbd>
            </span>
          )}
        </button>

        {/* Theme toggle */}
        <button
          aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
          onClick={toggleTheme}
          className={cn(
            "flex items-center gap-3 rounded-md px-3 py-1.5",
            "text-xs text-muted-foreground",
            "transition-colors",
            "hover:bg-accent hover:text-accent-foreground"
          )}
        >
          {theme === "dark" ? (
            <Sun size={14} strokeWidth={1.8} className="shrink-0" />
          ) : (
            <Moon size={14} strokeWidth={1.8} className="shrink-0" />
          )}
          {!collapsed && <span>{theme === "dark" ? "Light mode" : "Dark mode"}</span>}
        </button>

        {/* Collapse toggle — desktop only */}
        {!onNavigate && (
          <button
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            onClick={toggleSidebar}
            className={cn(
              "flex items-center gap-3 rounded-md px-3 py-1.5",
              "text-xs text-muted-foreground",
              "transition-colors",
              "hover:bg-accent hover:text-accent-foreground"
            )}
          >
            {collapsed ? (
              <ChevronsRight size={14} strokeWidth={1.8} className="shrink-0" />
            ) : (
              <ChevronsLeft size={14} strokeWidth={1.8} className="shrink-0" />
            )}
            {!collapsed && <span>Collapse</span>}
          </button>
        )}
      </div>
    </>
  );
}

export function Sidebar() {
  const sidebarCollapsed = useUiStore((s) => s.sidebarCollapsed);

  return (
    <TooltipProvider delayDuration={0}>
      <aside
        className={cn(
          "hidden md:flex fixed inset-y-0 left-0 z-30 flex-col border-r",
          "bg-[var(--sidebar-bg)] border-[var(--sidebar-border)]",
        )}
        style={{
          width: sidebarCollapsed ? 56 : 240,
          transition: "width 200ms cubic-bezier(0.16, 1, 0.3, 1)",
        }}
      >
        <SidebarContent collapsed={sidebarCollapsed} />
      </aside>
    </TooltipProvider>
  );
}
