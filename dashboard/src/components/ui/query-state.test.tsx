import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryState } from "./query-state";
import type { UseQueryResult } from "@tanstack/react-query";

function mockQuery(overrides: Partial<UseQueryResult<string>>): UseQueryResult<string> {
  return {
    data: undefined,
    error: null,
    isLoading: false,
    isError: false,
    isSuccess: false,
    isFetching: false,
    isPending: false,
    isRefetching: false,
    isLoadingError: false,
    isRefetchError: false,
    isStale: false,
    isFetched: false,
    isFetchedAfterMount: false,
    isPlaceholderData: false,
    isInitialLoading: false,
    status: "pending",
    fetchStatus: "idle",
    dataUpdatedAt: 0,
    errorUpdatedAt: 0,
    failureCount: 0,
    failureReason: null,
    errorUpdateCount: 0,
    refetch: vi.fn(),
    promise: Promise.resolve("" as string),
    ...overrides,
  } as unknown as UseQueryResult<string>;
}

describe("QueryState", () => {
  it("shows loading node when isLoading", () => {
    render(
      <QueryState query={mockQuery({ isLoading: true })} loading={<div>Loading...</div>}>
        {(data) => <div>{data}</div>}
      </QueryState>
    );
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("shows error UI when isError", () => {
    render(
      <QueryState
        query={mockQuery({ isError: true, error: new Error("Network fail") })}
        loading={<div>Loading...</div>}
      >
        {(data) => <div>{data}</div>}
      </QueryState>
    );
    expect(screen.getByText("Network fail")).toBeInTheDocument();
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });

  it("calls refetch on retry click", async () => {
    const refetch = vi.fn();
    render(
      <QueryState
        query={mockQuery({ isError: true, error: new Error("fail"), refetch })}
        loading={<div>Loading...</div>}
      >
        {(data) => <div>{data}</div>}
      </QueryState>
    );
    await userEvent.click(screen.getByText("Retry"));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("renders children with data when query succeeds", () => {
    render(
      <QueryState
        query={mockQuery({ data: "hello", isSuccess: true })}
        loading={<div>Loading...</div>}
      >
        {(data) => <div>Result: {data}</div>}
      </QueryState>
    );
    expect(screen.getByText("Result: hello")).toBeInTheDocument();
  });

  it("shows loading when data is undefined and not error", () => {
    render(
      <QueryState
        query={mockQuery({ data: undefined, isLoading: false, isError: false })}
        loading={<div>Loading...</div>}
      >
        {(data) => <div>{data}</div>}
      </QueryState>
    );
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });
});
