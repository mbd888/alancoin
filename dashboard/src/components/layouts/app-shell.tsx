import type { ReactNode } from "react";
import { Menu } from "lucide-react";
import { Sidebar, SidebarContent } from "@/components/global/sidebar";
import { CommandPalette } from "@/components/global/command-palette";
import { GlobalShortcuts } from "@/components/global/global-shortcuts";
import { ErrorBoundary } from "@/components/ui/error-boundary";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { useUiStore } from "@/stores/ui-store";
import { useIsMobile } from "@/hooks/use-is-mobile";

export function AppShell({ children }: { children: ReactNode }) {
  const sidebarCollapsed = useUiStore((s) => s.sidebarCollapsed);
  const mobileMenuOpen = useUiStore((s) => s.mobileMenuOpen);
  const setMobileMenuOpen = useUiStore((s) => s.setMobileMenuOpen);
  const isMobile = useIsMobile();

  return (
    <>
      <GlobalShortcuts />
      <CommandPalette />

      {/* Mobile top bar */}
      <div className="fixed inset-x-0 top-0 z-30 flex h-14 items-center gap-3 border-b bg-[var(--sidebar-bg)] px-4 md:hidden">
        <button
          aria-label="Open menu"
          onClick={() => setMobileMenuOpen(true)}
          className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        >
          <Menu size={18} />
        </button>
        <img src="/alancoin-icon.png" alt="Alancoin" className="size-6 rounded-md" />
        <span className="text-sm font-semibold text-foreground">Alancoin</span>
      </div>

      {/* Mobile sidebar drawer */}
      <Sheet open={mobileMenuOpen} onOpenChange={setMobileMenuOpen}>
        <SheetContent side="left" className="flex w-64 flex-col p-0 bg-[var(--sidebar-bg)]">
          <SidebarContent
            collapsed={false}
            onNavigate={() => setMobileMenuOpen(false)}
          />
        </SheetContent>
      </Sheet>

      <div className="flex h-screen overflow-hidden">
        <Sidebar />
        <main
          className="flex-1 overflow-y-auto bg-[var(--background)] pt-14 md:pt-0"
          style={
            isMobile
              ? undefined
              : {
                  marginLeft: sidebarCollapsed ? 56 : 240,
                  transition: "margin-left 200ms cubic-bezier(0.16, 1, 0.3, 1)",
                }
          }
        >
          <ErrorBoundary>{children}</ErrorBoundary>
        </main>
      </div>
    </>
  );
}
