import { useEffect } from "react";
import { useUiStore } from "@/stores/ui-store";

export function GlobalShortcuts() {
  const { toggleSidebar, setCommandPaletteOpen } = useUiStore();

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Don't capture when typing in inputs
      const tag = (e.target as HTMLElement)?.tagName;
      const isInput = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";

      // Cmd+K — command palette
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setCommandPaletteOpen(true);
        return;
      }

      // Don't process remaining shortcuts when focused on inputs
      if (isInput) return;

      // [ — toggle sidebar (Linear pattern)
      if (e.key === "[" && !e.metaKey && !e.ctrlKey) {
        toggleSidebar();
        return;
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [toggleSidebar, setCommandPaletteOpen]);

  return null;
}
