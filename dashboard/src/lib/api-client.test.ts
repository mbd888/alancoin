import { describe, it, expect, vi, beforeEach } from "vitest";
import { api, ApiError } from "./api-client";

describe("api client", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("makes GET requests with params", async () => {
    const mockResponse = { data: "test" };
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(mockResponse), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );

    const result = await api.get<{ data: string }>("/test", { foo: "bar" });

    expect(result).toEqual(mockResponse);
    const call = vi.mocked(fetch).mock.calls[0];
    expect(call[0]).toContain("/v1/test");
    expect(call[0]).toContain("foo=bar");
  });

  it("includes auth header when API key is stored", async () => {
    localStorage.setItem("alancoin_api_key", "test_key_123");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
    );

    await api.get("/test");

    const call = vi.mocked(fetch).mock.calls[0];
    const headers = call[1]?.headers as Record<string, string>;
    expect(headers["Authorization"]).toBe("Bearer test_key_123");
  });

  it("throws ApiError on non-OK response", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "not_found" }), {
        status: 404,
        statusText: "Not Found",
        headers: { "Content-Type": "application/json" },
      })
    );

    await expect(api.get("/missing")).rejects.toThrow(ApiError);
    await expect(
      api.get("/missing").catch((e) => {
        expect(e).toBeInstanceOf(ApiError);
        expect((e as ApiError).status).toBe(404);
        throw e;
      })
    ).rejects.toThrow();
  });

  it("sends JSON body on POST", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
    );

    await api.post("/create", { name: "test" });

    const call = vi.mocked(fetch).mock.calls[0];
    expect(call[1]?.body).toBe(JSON.stringify({ name: "test" }));
  });

  it("handles 204 No Content", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(null, { status: 204 })
    );

    const result = await api.delete("/item/1");
    expect(result).toBeUndefined();
  });
});
