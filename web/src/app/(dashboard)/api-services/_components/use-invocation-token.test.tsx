import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Token } from "@/lib/types";
import { createTestQueryClient, queryClientWrapper } from "@/test/render";

import { useInvocationToken, type InvocationTokenScope } from "./use-invocation-token";

const routeScope: InvocationTokenScope = {
  viewerUserID: 1,
  apiServiceID: 7,
  apiRouteID: 9,
};

const token: Token = {
  id: 5,
  user_id: 1,
  key: "sk-production-secret",
  name: "Production Token",
  status: 1,
  expired_at: 0,
  models: "",
  trace_enabled: false,
  trace_mode: "full",
  api_role_mode: "explicit",
  created_at: 1,
  updated_at: 1,
};

const viewerKey = (viewerID: number) => `aigw:api-catalog-token-id:${viewerID}`;
const routeKey = (scope: InvocationTokenScope) =>
  `aigw:api-invocation-token-id:${scope.viewerUserID}:${scope.apiServiceID}:${scope.apiRouteID}`;
const validationPath = (scope: InvocationTokenScope, tokenID = 5) =>
  `/api/tokens?usable_only=true&api_service_id=${scope.apiServiceID}&api_route_id=${scope.apiRouteID}&token_id=${tokenID}&page=1&page_size=1`;

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", "x-server-time-ms": String(Date.now()) },
  });
}

function success(item: Token = token) {
  return json({ data: [item], total: 1, page: 1, page_size: 1 });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function renderTokenHook(
  scope: InvocationTokenScope = routeScope,
  options?: Parameters<typeof useInvocationToken>[1],
) {
  const queryClient = createTestQueryClient();
  return {
    queryClient,
    ...renderHook(
      ({ currentScope, currentOptions }) => useInvocationToken(currentScope, currentOptions),
      {
        initialProps: { currentScope: scope, currentOptions: options },
        wrapper: queryClientWrapper(queryClient),
      },
    ),
  };
}

describe("useInvocationToken", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each([
    ["5", 5],
    [null, 0],
    ["0", 0],
    ["-1", 0],
    ["9007199254740992", 0],
    ["5x", 0],
  ])("parses viewer-scoped remembered value %s as %s", (stored, expected) => {
    if (stored !== null) window.localStorage.setItem(viewerKey(1), stored);

    const { result } = renderTokenHook(routeScope, { rememberScope: "viewer" });

    expect(result.current.tokenID).toBe(expected);
  });

  it("remembers only the currently verified Token with a matching ID", async () => {
    const pending = deferred<Response>();
    vi.mocked(fetch).mockImplementation(() => pending.promise);
    const { result } = renderTokenHook(routeScope, { rememberScope: "viewer" });

    act(() => result.current.setTokenID(5));
    act(() => result.current.rememberToken());
    expect(window.localStorage.getItem(viewerKey(1))).toBeNull();

    pending.resolve(success());
    await waitFor(() => expect(result.current.token?.id).toBe(5));
    act(() => result.current.rememberToken());

    expect(window.localStorage.getItem(viewerKey(1))).toBe("5");
  });

  it("revalidates the same viewer candidate per Route and drops the previous Route secret while pending", async () => {
    window.localStorage.setItem(viewerKey(1), "5");
    const routeB = { ...routeScope, apiRouteID: 10 };
    const pendingB = deferred<Response>();
    vi.mocked(fetch).mockImplementation((input) => {
      if (String(input) === validationPath(routeScope)) return Promise.resolve(success());
      if (String(input) === validationPath(routeB)) return pendingB.promise;
      return Promise.reject(new Error(`unexpected request: ${String(input)}`));
    });
    const { result, rerender } = renderTokenHook(routeScope, { rememberScope: "viewer" });
    await waitFor(() => expect(result.current.token?.key).toBe("sk-production-secret"));

    rerender({ currentScope: routeB, currentOptions: { rememberScope: "viewer" } });

    await waitFor(() => expect(fetch).toHaveBeenCalledWith(validationPath(routeB), expect.anything()));
    expect(result.current.tokenID).toBe(5);
    expect(result.current.isChecking).toBe(true);
    expect(result.current.token).toBeUndefined();
  });

  it.each([
    ["miss", () => Promise.resolve(json({ data: [], total: 0, page: 1, page_size: 1 })), "miss"],
    ["error", () => Promise.resolve(json({ error: "validation unavailable" }, 503)), "error"],
  ] as const)("fails closed and clears a remembered Token after validation %s", async (_name, response, failure) => {
    window.localStorage.setItem(viewerKey(1), "5");
    vi.mocked(fetch).mockImplementation(response);
    const { result } = renderTokenHook(routeScope, { rememberScope: "viewer" });

    await waitFor(() => expect(result.current.failure).toBe(failure));

    expect(result.current.token).toBeUndefined();
    expect(window.localStorage.getItem(viewerKey(1))).toBeNull();
  });

  it("treats protected storage read, write, and remove failures as fail-closed state", async () => {
    const originalGet = Storage.prototype.getItem;
    const originalSet = Storage.prototype.setItem;
    const originalRemove = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key: string) {
      if (key === viewerKey(1)) throw new DOMException("blocked", "SecurityError");
      return originalGet.call(this, key);
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === viewerKey(1)) throw new DOMException("quota", "QuotaExceededError");
      return originalSet.call(this, key, value);
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key: string) {
      if (key === viewerKey(1)) throw new DOMException("blocked", "SecurityError");
      return originalRemove.call(this, key);
    });
    vi.mocked(fetch).mockResolvedValue(success());
    const onValueChange = vi.fn();
    const { result } = renderTokenHook(routeScope, { value: 5, onValueChange, rememberScope: "viewer" });
    await waitFor(() => expect(result.current.token?.id).toBe(5));

    expect(() => act(() => result.current.rememberToken())).not.toThrow();
    expect(() => act(() => result.current.clearToken())).not.toThrow();
    expect(onValueChange).toHaveBeenCalledWith(0);
  });

  it("does not reuse an uncontrolled candidate or success after the viewer changes", async () => {
    vi.mocked(fetch).mockResolvedValue(success());
    const { result, rerender } = renderTokenHook(routeScope, { rememberScope: "viewer" });
    act(() => result.current.setTokenID(5));
    await waitFor(() => expect(result.current.token?.id).toBe(5));

    rerender({
      currentScope: { ...routeScope, viewerUserID: 2 },
      currentOptions: { rememberScope: "viewer" },
    });

    expect(result.current.tokenID).toBe(0);
    expect(result.current.token).toBeUndefined();
  });

  it("keeps controlled selection in the caller and preserves uncontrolled route-scoped storage", async () => {
    const onValueChange = vi.fn();
    const controlled = renderTokenHook(routeScope, { value: 5, onValueChange, rememberScope: "viewer" });
    act(() => controlled.result.current.setTokenID(8));
    expect(onValueChange).toHaveBeenCalledWith(8);
    expect(controlled.result.current.tokenID).toBe(5);
    controlled.unmount();

    window.localStorage.setItem(routeKey(routeScope), "5");
    vi.mocked(fetch).mockResolvedValue(success());
    const routeScoped = renderTokenHook(routeScope, { rememberScope: "route" });
    expect(routeScoped.result.current.tokenID).toBe(5);
    await waitFor(() => expect(routeScoped.result.current.token?.id).toBe(5));
  });

  it("offers a remembered viewer Token to an empty controlled owner without creating local selection state", async () => {
    window.localStorage.setItem(viewerKey(1), "5");
    const onValueChange = vi.fn();
    const { result } = renderTokenHook(routeScope, { value: 0, onValueChange, rememberScope: "viewer" });

    await waitFor(() => expect(onValueChange).toHaveBeenCalledWith(5));
    expect(result.current.tokenID).toBe(0);
  });

  it("revokes the real Token immediately when a background refetch changes success to error", async () => {
    window.localStorage.setItem(viewerKey(1), "5");
    const background = deferred<Response>();
    vi.mocked(fetch)
      .mockResolvedValueOnce(success())
      .mockImplementationOnce(() => background.promise);
    const { result, queryClient } = renderTokenHook(routeScope, { rememberScope: "viewer" });
    await waitFor(() => expect(result.current.token?.id).toBe(5));

    void queryClient.refetchQueries({
      queryKey: ["tokens", "usable-for-api-route", 1, 7, 9, 5],
    });
    await waitFor(() => expect(result.current.isChecking).toBe(true));
    expect(result.current.token).toBeUndefined();
    background.resolve(json({ error: "validation unavailable" }, 503));
    await waitFor(() => expect(result.current.failure).toBe("error"));
  });

  it.each([
    { ...routeScope, viewerUserID: 0 },
    { ...routeScope, apiServiceID: 0 },
    { ...routeScope, apiRouteID: 0 },
  ])("does not query an invalid scope $viewerUserID/$apiServiceID/$apiRouteID", (scope) => {
    const { result } = renderTokenHook(scope, { value: 5, rememberScope: "viewer" });

    expect(result.current.isChecking).toBe(false);
    expect(result.current.token).toBeUndefined();
    expect(fetch).not.toHaveBeenCalled();
  });
});
