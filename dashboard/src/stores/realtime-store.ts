import { create } from "zustand";
import type { RealtimeEvent } from "@/lib/types";

const MAX_EVENTS = 200;
const RECONNECT_DELAYS = [3000, 6000, 12000, 30000];

interface RealtimeState {
  connected: boolean;
  events: RealtimeEvent[];
  connectionError: string | null;
  reconnectAttempt: number;
  connect: () => void;
  disconnect: () => void;
  clearEvents: () => void;
}

let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

export const useRealtimeStore = create<RealtimeState>()((set, get) => ({
  connected: false,
  events: [],
  connectionError: null,
  reconnectAttempt: 0,

  connect: () => {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    const token = localStorage.getItem("alancoin_api_key") ?? "";
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsBase = import.meta.env.VITE_WS_URL || `${protocol}//${window.location.host}`;
    const url = `${wsBase}/ws?token=${encodeURIComponent(token)}`;

    try {
      ws = new WebSocket(url);
    } catch {
      set({ connectionError: "Failed to create WebSocket connection" });
      return;
    }

    ws.onopen = () => {
      set({ connected: true, connectionError: null, reconnectAttempt: 0 });
      // Subscribe to all events
      ws?.send(JSON.stringify({ allEvents: true }));
    };

    ws.onmessage = (e) => {
      try {
        const event: RealtimeEvent = JSON.parse(e.data);
        set((state) => {
          const events = [event, ...state.events];
          if (events.length > MAX_EVENTS) events.length = MAX_EVENTS;
          return { events };
        });
      } catch {
        // ignore malformed messages
      }
    };

    ws.onclose = () => {
      set({ connected: false });
      ws = null;
      // Auto-reconnect
      const attempt = get().reconnectAttempt;
      const delay = RECONNECT_DELAYS[Math.min(attempt, RECONNECT_DELAYS.length - 1)];
      set({ reconnectAttempt: attempt + 1 });
      reconnectTimer = setTimeout(() => get().connect(), delay);
    };

    ws.onerror = () => {
      set({ connectionError: "WebSocket error" });
    };
  },

  disconnect: () => {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (ws) {
      ws.close();
      ws = null;
    }
    set({ connected: false, reconnectAttempt: 0 });
  },

  clearEvents: () => set({ events: [] }),
}));
