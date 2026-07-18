import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentDetail, ConnectionSnapshot, ConnectionSummary } from "@/lib/types";
import {
  agentListPollInterval,
  agentQueryKeys,
  probeProgressPollInterval,
  useAgentOperation,
  useEnqueueConnectivityProbe,
  useUpdateAgent,
} from "@/lib/api/agents";
import { createTestQueryClient as makeTestQueryClient, queryClientWrapper } from "@/test/render";
import { useAgentConnections } from "./use-agent-connections";
import { useDocumentVisibility } from "./use-document-visibility";

function connection(seq: number): ConnectionSnapshot {
  return {
    version: "v1",
    snapshot_epoch: "epoch-a",
    snapshot_seq: seq,
    observed_at: seq,
    agent_id: "agent-a",
    admin_status: 1,
    control: {
      state: "connected",
      health: "healthy",
      reason_codes: [],
      session_generation: 1,
      connected_at: 1,
      heartbeat_at: 1,
      runtime_reported_at: 1,
      last_seen: 1,
    },
    relay: {
      support: "supported",
      config: "configured",
      availability: "ready",
      accepting_new_streams: true,
      convergence: "converged",
      desired: {
        mode: "inherit",
        configured_uri: "",
        effective_uri: "wss://relay.example/ws",
        desired_generation: 1,
      },
      active: {
        uri: "wss://relay.example/ws",
        active_generation: 1,
        session_generation: 1,
        connected_at: 1,
        streams: 0,
        retry_at: 0,
      },
      recent_errors: [],
    },
    direct: {
      summary: {
        state: "unknown",
        reachable: 0,
        degraded: 0,
        unreachable: 0,
        stale: 0,
        total: 0,
      },
      targets: {},
    },
    target_summaries: {
      direct: {
        state: "unknown",
        reachable: 0,
        degraded: 0,
        unreachable: 0,
        stale: 0,
        total: 0,
      },
      relay: {
        state: "unknown",
        reachable: 0,
        unreachable: 0,
        unavailable: 0,
        unknown: 0,
        unsupported: 0,
        stale: 0,
        total: 0,
      },
    },
    allowed_operations: [],
  };
}

function detail(value: ConnectionSnapshot): AgentDetail {
  return {
    id: 7,
    agent_id: "agent-a",
    name: "Agent A",
    status: 1,
    http_addresses: "[]",
    tags: "",
    proxy_url: "",
    relay_mode: "inherit",
    relay_uri: "",
    peer_route_mode: "direct_first",
    last_seen: 1,
    created_at: 1,
    runtime: null,
    connection: value,
    route_targets: {
      snapshot_epoch: value.snapshot_epoch,
      snapshot_seq: value.snapshot_seq,
      observed_at: value.observed_at,
      summaries: value.target_summaries,
      data: [],
      limit: 20,
    },
  };
}

function summary(
  value: ConnectionSnapshot,
  overrides: Partial<ConnectionSummary> = {},
): ConnectionSummary {
  return {
    version: "v1",
    snapshot_epoch: value.snapshot_epoch,
    snapshot_seq: value.snapshot_seq,
    observed_at: value.observed_at,
    control: value.control,
    relay: {
      support: value.relay.support,
      config: value.relay.config,
      availability: value.relay.availability,
      accepting_new_streams: value.relay.accepting_new_streams,
      convergence: value.relay.convergence,
      streams: value.relay.active.streams,
    },
    direct: value.direct.summary,
    targets: value.target_summaries,
    ...overrides,
  };
}

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    }),
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

const queryClients: ReturnType<typeof makeTestQueryClient>[] = [];

function createTestQueryClient() {
  const queryClient = makeTestQueryClient();
  queryClient.setDefaultOptions({
    queries: { gcTime: Infinity, retry: false },
    mutations: { gcTime: Infinity, retry: false },
  });
  queryClients.push(queryClient);
  return queryClient;
}

afterEach(async () => {
  await Promise.all(queryClients.map((queryClient) => queryClient.cancelQueries()));
  cleanup();
  for (const queryClient of queryClients.splice(0)) queryClient.clear();
  await act(async () => undefined);
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("useDocumentVisibility", () => {
  it("reports a visible document", () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");

    const { result } = renderHook(() => useDocumentVisibility());

    expect(result.current).toBe(true);
  });

  it("tracks hidden and visible transitions", () => {
    let visibility: DocumentVisibilityState = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
    const { result } = renderHook(() => useDocumentVisibility());

    visibility = "hidden";
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(result.current).toBe(false);

    visibility = "visible";
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(result.current).toBe(true);
  });

  it("removes its visibility listener on unmount", () => {
    const remove = vi.spyOn(document, "removeEventListener");
    const { unmount } = renderHook(() => useDocumentVisibility());

    unmount();

    expect(remove).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
  });
});

describe("useAgentConnections", () => {
  it("does not fetch until explicitly enabled", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() => jsonResponse(detail(connection(1))));
    const queryClient = createTestQueryClient();
    const { rerender } = renderHook(
      ({ enabled }) => useAgentConnections(7, undefined, { enabled }),
      { initialProps: { enabled: false }, wrapper: queryClientWrapper(queryClient) },
    );

    expect(fetchMock).not.toHaveBeenCalled();
    rerender({ enabled: true });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
  });

  it("accepts the connection snapshot and direct first page as one detail result", async () => {
    const first = detail(connection(10));
    first.route_targets.next_cursor = "cursor-10";
    const second = detail(connection(11));
    second.route_targets.next_cursor = "cursor-11";
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(first))
      .mockImplementationOnce(() => jsonResponse(second));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(
      () => useAgentConnections(7),
      { wrapper: queryClientWrapper(queryClient) },
    );

    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));
    expect(result.current.routeTargetsPage).toMatchObject({
      snapshot_epoch: "epoch-a",
      snapshot_seq: 10,
      next_cursor: "cursor-10",
    });

    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(11));
    expect(result.current.routeTargetsPage).toMatchObject({
      snapshot_epoch: "epoch-a",
      snapshot_seq: 11,
      next_cursor: "cursor-11",
    });
  });

  it("accepts a Direct first page keyed by content generation instead of parent sequence", async () => {
    const current = connection(10);
    current.direct.generation = 3;
    const response = detail(current);
    response.route_targets.snapshot_seq = 3;
    vi.spyOn(globalThis, "fetch").mockImplementationOnce(() => jsonResponse(response));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), { wrapper: queryClientWrapper(queryClient) });

    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));
    expect(result.current.routeTargetsPage?.snapshot_seq).toBe(3);
  });

  it("does not let an in-flight older detail overwrite a newer summary", async () => {
    const detailSnapshot = connection(10);
    const newerSummary = summary(detailSnapshot, {
      snapshot_seq: 11,
      observed_at: 11,
    });
    const pendingResponse = deferred<Response>();
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(detailSnapshot)))
      .mockImplementationOnce(() => pendingResponse.promise);
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ listSummary }: { listSummary?: ConnectionSummary }) =>
        useAgentConnections(7, listSummary),
      {
        initialProps: { listSummary: undefined as ConnectionSummary | undefined },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));

    const pendingRefetch = result.current.refetch();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    rerender({ listSummary: newerSummary });
    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(11));
    pendingResponse.resolve(await jsonResponse(detail(detailSnapshot)));
    await act(async () => void (await pendingRefetch));

    expect(result.current.data?.snapshot_seq).toBe(11);
    expect(
      queryClient.getQueryData<{ current?: ConnectionSnapshot }>(
        agentQueryKeys.connections(7),
      )?.current?.snapshot_seq,
    ).toBe(11);
    expect(result.current.stale).toBe(false);
  });

  it("adds stale without rolling back a summary when an in-flight detail fails", async () => {
    const detailSnapshot = connection(10);
    const newerSummary = summary(detailSnapshot, {
      snapshot_seq: 11,
      observed_at: 11,
    });
    const pendingResponse = deferred<Response>();
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(detailSnapshot)))
      .mockImplementationOnce(() => pendingResponse.promise);
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ listSummary }: { listSummary?: ConnectionSummary }) =>
        useAgentConnections(7, listSummary),
      {
        initialProps: { listSummary: undefined as ConnectionSummary | undefined },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));

    const pendingRefetch = result.current.refetch();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    rerender({ listSummary: newerSummary });
    await waitFor(() => expect(result.current.data?.snapshot_seq).toBe(11));
    pendingResponse.reject(new Error("network unavailable"));
    await act(async () => void (await pendingRefetch));

    expect(result.current.data?.snapshot_seq).toBe(11);
    expect(
      queryClient.getQueryData<{ current?: ConnectionSnapshot; stale: boolean }>(
        agentQueryKeys.connections(7),
      ),
    ).toMatchObject({ current: { snapshot_seq: 11 }, stale: true });
    await waitFor(() => expect(result.current.stale).toBe(true));
  });

  it("polls a stable connection every 15 seconds", async () => {
    vi.useFakeTimers();
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() => jsonResponse(detail(connection(10))));
    const queryClient = createTestQueryClient();
    renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await act(async () => vi.advanceTimersByTimeAsync(14_999));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(1));

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("uses an initial snapshot without a duplicate mount or window-focus request", async () => {
    vi.useFakeTimers();
    const initial = connection(10);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() => jsonResponse(detail(initial)));
    const queryClient = createTestQueryClient();
    renderHook(
      () => useAgentConnections(7, undefined, { initialSnapshot: initial }),
      { wrapper: queryClientWrapper(queryClient) },
    );

    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(fetchMock).not.toHaveBeenCalled();
    act(() => window.dispatchEvent(new Event("focus")));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(14_999));
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(1));
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  });

  it("stores a newer applying summary and polls it every 3 seconds", async () => {
    vi.useFakeTimers();
    const detailSnapshot = connection(10);
    const applying = summary(detailSnapshot, {
      snapshot_seq: 11,
      observed_at: 11,
      relay: { ...summary(detailSnapshot).relay, convergence: "applying" },
    });
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() => jsonResponse(detail(detailSnapshot)));
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ listSummary }: { listSummary?: ConnectionSummary }) =>
        useAgentConnections(7, listSummary),
      {
        initialProps: { listSummary: undefined as ConnectionSummary | undefined },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await vi.waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));

    rerender({ listSummary: applying });
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.data?.snapshot_seq).toBe(11);
    rerender({ listSummary: undefined });
    expect(result.current.data?.snapshot_seq).toBe(11);

    await act(async () => vi.advanceTimersByTimeAsync(2_999));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("replaces an applying list summary with a newer converged detail snapshot", async () => {
    vi.useFakeTimers();
    const first = connection(10);
    const applying = summary(first, {
      snapshot_seq: 11,
      observed_at: 11,
      relay: { ...summary(first).relay, convergence: "applying" },
    });
    const converged = connection(12);
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockImplementation(() => jsonResponse(detail(converged)));
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ listSummary }: { listSummary?: ConnectionSummary }) =>
        useAgentConnections(7, listSummary),
      {
        initialProps: { listSummary: undefined as ConnectionSummary | undefined },
        wrapper: queryClientWrapper(queryClient),
      },
    );
    await vi.waitFor(() => expect(result.current.data?.snapshot_seq).toBe(10));

    rerender({ listSummary: applying });
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.data?.relay.convergence).toBe("applying");
    await act(async () => vi.advanceTimersByTimeAsync(3_000));
    await vi.waitFor(() => expect(result.current.data?.snapshot_seq).toBe(12));
    expect(result.current.data?.relay.convergence).toBe("converged");

    await act(async () => vi.advanceTimersByTimeAsync(3_000));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("pauses polling while hidden and resumes after becoming visible", async () => {
    vi.useFakeTimers();
    let visibility: DocumentVisibilityState = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() => jsonResponse(detail(connection(10))));
    const queryClient = createTestQueryClient();
    renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    visibility = "hidden";
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await act(async () => vi.advanceTimersByTimeAsync(30_000));
    expect(fetchMock).toHaveBeenCalledTimes(1);

    visibility = "visible";
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await act(async () => vi.advanceTimersByTimeAsync(15_000));
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("retains the last successful snapshot and marks it stale after an API error", async () => {
    const first = connection(4);
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockRejectedValueOnce(new Error("network unavailable"));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });

    await waitFor(() => expect(result.current.data).toEqual(first));
    expect(result.current.stale).toBe(false);

    await act(async () => {
      await result.current.refetch();
    });

    await waitFor(() => expect(result.current.stale).toBe(true));
    expect(result.current.data).toEqual(first);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("clears stale only when a later successful snapshot is accepted", async () => {
    const first = connection(4);
    const later = connection(5);
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockImplementationOnce(() => jsonResponse(detail(later)));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await waitFor(() => expect(result.current.data).toEqual(first));

    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.stale).toBe(true));
    await act(async () => void (await result.current.refetch()));

    await waitFor(() => expect(result.current.data).toEqual(later));
    expect(result.current.stale).toBe(false);
  });

  it("keeps stale when a successful response is older than the retained snapshot", async () => {
    const first = connection(4);
    const older = connection(3);
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockImplementationOnce(() => jsonResponse(detail(older)));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await waitFor(() => expect(result.current.data).toEqual(first));

    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.stale).toBe(true));
    await act(async () => void (await result.current.refetch()));

    await waitFor(() => expect(result.current.isFetching).toBe(false));
    expect(result.current.data).toEqual(first);
    expect(result.current.stale).toBe(true);
  });

  it("keeps stale when a retired epoch arrives after a network error", async () => {
    const first = connection(10);
    const restarted = {
      ...connection(1),
      snapshot_epoch: "epoch-b",
      observed_at: 20,
    };
    const retiredLate = {
      ...connection(99),
      snapshot_epoch: "epoch-a",
      observed_at: 30,
    };
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockImplementationOnce(() => jsonResponse(detail(restarted)))
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockImplementationOnce(() => jsonResponse(detail(retiredLate)));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await waitFor(() => expect(result.current.data).toEqual(first));
    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.data).toEqual(restarted));
    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.stale).toBe(true));

    await act(async () => void (await result.current.refetch()));

    expect(result.current.data).toEqual(restarted);
    expect(result.current.stale).toBe(true);
  });

  it("does not mark retained data stale when a request is aborted", async () => {
    const first = connection(4);
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockRejectedValueOnce(new DOMException("request aborted", "AbortError"));
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(7), {
      wrapper: queryClientWrapper(queryClient),
    });
    await waitFor(() => expect(result.current.data).toEqual(first));

    await act(async () => void (await result.current.refetch()));

    expect(result.current.data).toEqual(first);
    expect(result.current.stale).toBe(false);
  });

  it("does not write stale state into the old agent cache after an id switch aborts fetch", async () => {
    const first = connection(4);
    const second = { ...connection(1), agent_id: "agent-b", snapshot_epoch: "epoch-b" };
    let startedRefetch: (() => void) | undefined;
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockImplementationOnce((_input, init) => {
        startedRefetch?.();
        return new Promise((_resolve, reject) => {
          init?.signal?.addEventListener(
            "abort",
            () => reject(new DOMException("request aborted", "AbortError")),
            { once: true },
          );
        });
      })
      .mockImplementationOnce(() => jsonResponse(detail(second)));
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ id }) => useAgentConnections(id),
      { initialProps: { id: 7 }, wrapper: queryClientWrapper(queryClient) },
    );
    await waitFor(() => expect(result.current.data).toEqual(first));

    const refetchStarted = new Promise<void>((resolve) => {
      startedRefetch = resolve;
    });
    const pendingRefetch = result.current.refetch();
    await refetchStarted;
    rerender({ id: 8 });
    await act(async () => void (await pendingRefetch));

    await waitFor(() => expect(result.current.data).toEqual(second));
    expect(
      queryClient.getQueryData<{ stale: boolean }>(agentQueryKeys.connections(7))?.stale,
    ).toBe(false);
  });

  it("isolates retained state when the agent id changes or becomes absent", async () => {
    const first = connection(4);
    const second = { ...connection(1), agent_id: "agent-b", snapshot_epoch: "epoch-b" };
    vi.spyOn(globalThis, "fetch")
      .mockImplementationOnce(() => jsonResponse(detail(first)))
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockImplementationOnce(() => jsonResponse(detail(second)));
    const queryClient = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ id }) => useAgentConnections(id),
      { initialProps: { id: 7 }, wrapper: queryClientWrapper(queryClient) },
    );
    await waitFor(() => expect(result.current.data).toEqual(first));
    await act(async () => void (await result.current.refetch()));
    await waitFor(() => expect(result.current.stale).toBe(true));

    rerender({ id: 0 });
    expect(result.current.data).toBeUndefined();
    expect(result.current.stale).toBe(false);

    rerender({ id: 8 });
    await waitFor(() => expect(result.current.data).toEqual(second));
    expect(result.current.stale).toBe(false);
  });

  it("does not fetch when the agent id is absent", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    const queryClient = createTestQueryClient();
    const { result } = renderHook(() => useAgentConnections(0), {
      wrapper: queryClientWrapper(queryClient),
    });

    expect(result.current.data).toBeUndefined();
    expect(result.current.stale).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("agent operation invalidation", () => {
  it("invalidates the edited Agent views without invalidating another Agent", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse({ id: 7, name: "Agent A", relay_mode: "custom", relay_uri: "wss://relay" }),
    );
    const queryClient = createTestQueryClient();
    const currentKeys = [
      agentQueryKeys.list({ page: 1 }),
      agentQueryKeys.detail(7),
      agentQueryKeys.connections(7),
      agentQueryKeys.targets(7),
      agentQueryKeys.progress(7, "probe-1"),
    ] as const;
    currentKeys.forEach((key) => queryClient.setQueryData(key, { seeded: true }));
    queryClient.setQueryData(agentQueryKeys.connections(8), { seeded: true });
    const { result } = renderHook(() => useUpdateAgent(), {
      wrapper: queryClientWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({ id: 7, relay_mode: "custom", relay_uri: "wss://relay" });
    });

    for (const key of currentKeys) {
      expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    }
    expect(queryClient.getQueryState(agentQueryKeys.connections(8))?.isInvalidated).toBe(false);
  });

  it("invalidates all connection views after a probe is accepted", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse(
        {
          probe_id: "probe-1",
          probe_generation: 2,
          scope: { kind: "all_enabled" },
          state: "queued",
          target_total: 3,
          snapshot_seq: 9,
        },
        202,
      ),
    );
    const queryClient = createTestQueryClient();
    const keys = [
      agentQueryKeys.list({ page: 1 }),
      agentQueryKeys.detail(7),
      agentQueryKeys.connections(7),
      agentQueryKeys.targets(7),
      agentQueryKeys.progress(7, "probe-1"),
    ] as const;
    keys.forEach((key) => queryClient.setQueryData(key, { seeded: true }));
    const { result } = renderHook(() => useEnqueueConnectivityProbe(), {
      wrapper: queryClientWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({
        id: 7,
        request: { expected_epoch: "epoch-a", expected_control_generation: 1 },
      });
    });

    for (const key of keys) {
      expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    }
  });

  it("invalidates list, detail, connections, direct, and progress after acceptance", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse({ operation_id: "operation-1", state: "accepted", snapshot_seq: 9 }, 202),
    );
    const queryClient = createTestQueryClient();
    const keys = [
      agentQueryKeys.list({ page: 1 }),
      agentQueryKeys.detail(7),
      agentQueryKeys.connections(7),
      agentQueryKeys.targets(7),
      agentQueryKeys.progress(7, "probe-1"),
    ] as const;
    keys.forEach((key) => queryClient.setQueryData(key, { seeded: true }));
    const { result } = renderHook(() => useAgentOperation(), {
      wrapper: queryClientWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({
        id: 7,
        operation: "relay_reconnect",
        request: { expected_epoch: "epoch-a", expected_control_generation: 1 },
      });
    });

    for (const key of keys) {
      expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    }
  });

  it("does not invalidate cached data when the operation is rejected", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse({ error: "generation changed" }, 409),
    );
    const queryClient = createTestQueryClient();
    const key = agentQueryKeys.connections(7);
    queryClient.setQueryData(key, { seeded: true });
    const { result } = renderHook(() => useAgentOperation(), {
      wrapper: queryClientWrapper(queryClient),
    });

    await expect(
      result.current.mutateAsync({
        id: 7,
        operation: "relay_drain",
        request: { expected_epoch: "epoch-a", expected_relay_generation: 1 },
      }),
    ).rejects.toThrow("generation changed");

    expect(queryClient.getQueryState(key)?.isInvalidated).toBe(false);
  });

  it("invalidates only the mutated agent's scoped queries", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse({ operation_id: "operation-1", state: "accepted", snapshot_seq: 9 }, 202),
    );
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(agentQueryKeys.connections(7), { seeded: true });
    queryClient.setQueryData(agentQueryKeys.connections(8), { seeded: true });
    const { result } = renderHook(() => useAgentOperation(), {
      wrapper: queryClientWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({
        id: 7,
        operation: "full_sync",
        request: { expected_epoch: "epoch-a", expected_control_generation: 1 },
      });
    });

    expect(queryClient.getQueryState(agentQueryKeys.connections(7))?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(agentQueryKeys.connections(8))?.isInvalidated).toBe(false);
  });
});

describe("agent list polling", () => {
  it("uses the stable interval before the first response", () => {
    expect(agentListPollInterval(undefined)).toBe(15_000);
  });

  it("uses the stable interval for converged healthy rows", () => {
    expect(agentListPollInterval([
      { connection: summary(connection(1)) },
    ])).toBe(15_000);
  });

  it("uses the transient interval only while a Relay change is applying", () => {
    const current = summary(connection(1));
    expect(agentListPollInterval([
      { connection: { ...current, relay: { ...current.relay, convergence: "applying" } } },
    ])).toBe(3_000);
    expect(agentListPollInterval([
      { connection: { ...current, control: { ...current.control, health: "degraded" } } },
    ])).toBe(15_000);
  });
});

describe("probe progress polling", () => {
  it("polls before the first progress response", () => {
    expect(probeProgressPollInterval(undefined)).toBe(3_000);
  });

  it.each(["queued", "running"] as const)("polls while progress is %s", (state) => {
    expect(probeProgressPollInterval({ state })).toBe(3_000);
  });

  it.each(["completed", "failed", "cancelled"] as const)(
    "stops polling when progress is %s",
    (state) => {
      expect(probeProgressPollInterval({ state })).toBe(false);
    },
  );
});
