import { describe, it, expect, beforeEach } from "vitest";
import { useUiStore } from "./ui-store";

describe("ui store", () => {
  beforeEach(() => {
    // Reset store to defaults
    useUiStore.setState({
      sidebarCollapsed: false,
      theme: "dark",
      commandPaletteOpen: false,
    });
  });

  it("starts with sidebar expanded", () => {
    expect(useUiStore.getState().sidebarCollapsed).toBe(false);
  });

  it("toggles sidebar", () => {
    useUiStore.getState().toggleSidebar();
    expect(useUiStore.getState().sidebarCollapsed).toBe(true);

    useUiStore.getState().toggleSidebar();
    expect(useUiStore.getState().sidebarCollapsed).toBe(false);
  });

  it("sets sidebar collapsed directly", () => {
    useUiStore.getState().setSidebarCollapsed(true);
    expect(useUiStore.getState().sidebarCollapsed).toBe(true);
  });

  it("toggles command palette", () => {
    expect(useUiStore.getState().commandPaletteOpen).toBe(false);

    useUiStore.getState().setCommandPaletteOpen(true);
    expect(useUiStore.getState().commandPaletteOpen).toBe(true);

    useUiStore.getState().setCommandPaletteOpen(false);
    expect(useUiStore.getState().commandPaletteOpen).toBe(false);
  });

  it("defaults to dark theme", () => {
    expect(useUiStore.getState().theme).toBe("dark");
  });
});
