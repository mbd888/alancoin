import { create } from "zustand";

const API_BASE = import.meta.env.VITE_API_URL || "/v1";

interface AuthState {
  apiKey: string | null;
  agentAddress: string | null;
  keyName: string | null;
  isAuthenticated: boolean;
  isValidating: boolean;
  error: string | null;
  login: (key: string) => Promise<boolean>;
  logout: () => void;
  restore: () => Promise<void>;
}

export const useAuthStore = create<AuthState>()((set, get) => ({
  apiKey: null,
  agentAddress: null,
  keyName: null,
  isAuthenticated: false,
  isValidating: true,
  error: null,

  login: async (key: string) => {
    set({ isValidating: true, error: null });
    localStorage.setItem("alancoin_api_key", key);

    try {
      const res = await fetch(`${API_BASE}/auth/me`, {
        headers: { Authorization: `Bearer ${key}` },
      });

      if (!res.ok) {
        localStorage.removeItem("alancoin_api_key");
        set({
          isValidating: false,
          isAuthenticated: false,
          error: res.status === 401 ? "Invalid API key" : `Server error (${res.status})`,
        });
        return false;
      }

      const data = await res.json();
      set({
        apiKey: key,
        agentAddress: data.agentAddress,
        keyName: data.keyName,
        isAuthenticated: true,
        isValidating: false,
        error: null,
      });
      return true;
    } catch {
      localStorage.removeItem("alancoin_api_key");
      set({
        isValidating: false,
        isAuthenticated: false,
        error: "Cannot reach server",
      });
      return false;
    }
  },

  logout: () => {
    localStorage.removeItem("alancoin_api_key");
    set({
      apiKey: null,
      agentAddress: null,
      keyName: null,
      isAuthenticated: false,
      isValidating: false,
      error: null,
    });
  },

  restore: async () => {
    if (get().apiKey) return;
    const stored = localStorage.getItem("alancoin_api_key");
    if (!stored) {
      set({ isValidating: false });
      return;
    }
    await get().login(stored);
  },
}));
