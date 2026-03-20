import type { ReactNode } from "react";
import { Sidebar } from "@/components/global/sidebar";
import { CommandPalette } from "@/components/global/command-palette";
import { GlobalShortcuts } from "@/components/global/global-shortcuts";
import { ErrorBoundary } from "@/components/ui/error-boundary";
import { useUiStore } from "@/stores/ui-store";

export function AppShell({ children }: { children: ReactNode }) {
  const sidebarCollapsed = useUiStore((s) => s.sidebarCollapsed);

  return (
    <>
      <GlobalShortcuts />
      <CommandPalette />
      <div className="flex h-screen overflow-hidden">
        <Sidebar />
        <main
          className="flex-1 overflow-y-auto bg-[var(--background)]"
          style={{
            marginLeft: sidebarCollapsed ? 56 : 240,
            transition: "margin-left 200ms cubic-bezier(0.16, 1, 0.3, 1)",
          }}
        >
          <ErrorBoundary>{children}</ErrorBoundary>
        </main>
      </div>
    </>
  );
}
