/**
 * Typed API client for the Alancoin Go backend.
 * All requests go through the Vite proxy (/v1 -> localhost:8080).
 */

const API_BASE = "/v1";

class ApiError extends Error {
  status: number;
  statusText: string;
  body: unknown;

  constructor(status: number, statusText: string, body: unknown) {
    super(`${status} ${statusText}`);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.body = body;
  }
}

async function request<T>(
  method: string,
  path: string,
  opts?: { body?: unknown; params?: Record<string, string> }
): Promise<T> {
  const url = new URL(`${API_BASE}${path}`, window.location.origin);
  if (opts?.params) {
    for (const [k, v] of Object.entries(opts.params)) {
      if (v) url.searchParams.set(k, v);
    }
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  const token = localStorage.getItem("alancoin_api_key");
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(url.toString(), {
    method,
    headers,
    body: opts?.body ? JSON.stringify(opts.body) : undefined,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw new ApiError(res.status, res.statusText, body);
  }

  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  get: <T>(path: string, params?: Record<string, string>) =>
    request<T>("GET", path, { params }),
  post: <T>(path: string, body?: unknown) =>
    request<T>("POST", path, { body }),
  put: <T>(path: string, body?: unknown) =>
    request<T>("PUT", path, { body }),
  delete: <T>(path: string) => request<T>("DELETE", path),
};

export { ApiError };
