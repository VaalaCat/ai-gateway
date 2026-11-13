import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useAllInflight, useBreakerBoard, useDeliveryBoard, useLimiterUsage } from "./observability";

const hooks = [{ name: "inflight", useMonitor: useAllInflight }, { name: "breaker", useMonitor: useBreakerBoard }, { name: "delivery", useMonitor: useDeliveryBoard }, { name: "limiter", useMonitor: useLimiterUsage }];
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("monitoring polling cancellation", () => {
  it.each(hooks)("cancels an outstanding $name read when paused and permits a manual refresh", async ({ useMonitor }) => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal("fetch", vi.fn((_url: string, options: RequestInit) => new Promise<Response>((_resolve, reject) => {
      const signal = options.signal!;
      signals.push(signal);
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    })));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result, rerender, unmount } = renderHook(({ paused }) => useMonitor({ paused }), { initialProps: { paused: false }, wrapper });
    await waitFor(() => expect(signals).toHaveLength(1));
    rerender({ paused: true });
    await waitFor(() => expect(signals[0].aborted).toBe(true));
    expect(result.current.isFetching).toBe(false);
    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ requests: [], successful_agent_ids: [], failed_agents: [] })));
    await act(async () => { await result.current.refetch(); });
    expect(fetch).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(result.current.dataUpdatedAt).toBeGreaterThan(0));
    unmount(); client.clear();
  });

  it("reports the scoped monitoring timeout instead of silently retrying forever", async () => {
    const timeout = new AbortController();
    vi.spyOn(AbortSignal, "timeout").mockReturnValue(timeout.signal);
    vi.stubGlobal("fetch", vi.fn((_url: string, options: RequestInit) => new Promise<Response>((_resolve, reject) => {
      options.signal!.addEventListener("abort", () => reject(options.signal!.reason), { once: true });
    })));
    const client = new QueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result, unmount } = renderHook(() => useAllInflight(), { wrapper });
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    await act(async () => { timeout.abort(new DOMException("Monitoring timeout", "TimeoutError")); });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.isFetching).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(AbortSignal.timeout).toHaveBeenCalledWith(20_000);
    unmount(); client.clear();
  });
});
