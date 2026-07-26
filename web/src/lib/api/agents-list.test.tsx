import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import type { AgentListItem, PaginatedResponse } from "@/lib/types";
import { useAgents } from "./agents";

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function agent(id: number): AgentListItem {
  return {
    id,
    name: `Agent ${id}`,
    connection: { relay: { convergence: "converged" } },
  } as AgentListItem;
}

function page(data: AgentListItem[], pageNumber: number): PaginatedResponse<AgentListItem> {
  return { data, total: data.length, page: pageNumber, page_size: 10 };
}

function jsonResponse(value: unknown) {
  return Promise.resolve(new Response(JSON.stringify(value), {
    status: 200,
    headers: { "content-type": "application/json" },
  }));
}

describe("useAgents list transitions", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not retain a previous search result by default", async () => {
    const nextSearch = deferred<Response>();
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      String(input).includes("search=beta")
        ? nextSearch.promise
        : jsonResponse(page([agent(1)], 1)),
    );
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ search }) => useAgents({ search, page_size: 10 }),
      {
        initialProps: { search: "alpha" },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    rerender({ search: "beta" });
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    expect(result.current.data).toBeUndefined();
    expect(result.current.isPlaceholderData).toBe(false);

    act(() => nextSearch.resolve(new Response(JSON.stringify({ error: "search failed" }), {
      status: 500,
      headers: { "content-type": "application/json" },
    })));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  it("keeps page 1 visible while page 2 is pending", async () => {
    const page2 = deferred<Response>();
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      String(input).includes("page=2")
        ? page2.promise
        : jsonResponse(page([agent(1)], 1)),
    );
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ pageNumber }) => useAgents(
        { page: pageNumber, page_size: 10 },
        { retainPreviousData: true },
      ),
      {
        initialProps: { pageNumber: 1 },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    rerender({ pageNumber: 2 });
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));

    expect(result.current.data?.data.map((row) => row.id)).toEqual([1]);
    expect(result.current.isPlaceholderData).toBe(true);
  });

  it("keeps page 1 visible when loading page 2 fails", async () => {
    const page2 = deferred<Response>();
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      String(input).includes("page=2")
        ? page2.promise
        : jsonResponse(page([agent(1)], 1)),
    );
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ pageNumber }) => useAgents(
        { page: pageNumber, page_size: 10 },
        { retainPreviousData: true },
      ),
      {
        initialProps: { pageNumber: 1 },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    rerender({ pageNumber: 2 });
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    await act(async () => page2.reject(new Error("page 2 failed")));
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(result.current.data?.data.map((row) => row.id)).toEqual([1]);
  });

  it("replaces page 1 with a successful empty page 2", async () => {
    const page2 = deferred<Response>();
    vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
      String(input).includes("page=2")
        ? page2.promise
        : jsonResponse(page([agent(1)], 1)),
    );
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ pageNumber }) => useAgents(
        { page: pageNumber, page_size: 10 },
        { retainPreviousData: true },
      ),
      {
        initialProps: { pageNumber: 1 },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    rerender({ pageNumber: 2 });
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    const response = await jsonResponse(page([], 2));
    act(() => page2.resolve(response));
    await waitFor(() => expect(result.current.data?.data).toEqual([]));

    expect(result.current.data?.data).toEqual([]);
    expect(result.current.isPlaceholderData).toBe(false);
  });
});
