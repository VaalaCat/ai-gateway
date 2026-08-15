import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import type { Token } from "@/lib/types";
import { useMarketplaceTokenSelection } from "./use-marketplace-token-selection";

const state = vi.hoisted(() => ({
  query: "",
  bootstrapData: undefined as
    | { data: Token[]; total: number; page: number; page_size: number }
    | undefined,
  bootstrapLoading: false,
  bootstrapError: false,
  selectedTokens: new Map<number, Token>(),
  selectedLoadingIds: new Set<number>(),
  selectedFetchingIds: new Set<number>(),
  selectedErrors: new Map<number, unknown>(),
  clockNow: 1_000,
  replace: vi.fn(),
  tokenList: vi.fn(),
  tokenListOptions: vi.fn(),
  tokenOne: vi.fn(),
  expiryClock: vi.fn(),
  refetch: vi.fn(),
  selectedRefetch: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useSearchParams: () => new URLSearchParams(state.query),
  useRouter: () => ({ replace: state.replace }),
}));

vi.mock("@/lib/api/tokens", () => ({
  useTokens: (params: unknown, options: unknown) => {
    state.tokenList(params);
    state.tokenListOptions(options);
    return {
      data: state.bootstrapData,
      isLoading: state.bootstrapLoading,
      isError: state.bootstrapError,
      refetch: state.refetch,
    };
  },
  useToken: (id: number) => {
    state.tokenOne(id);
    return {
      data: state.selectedTokens.get(id),
      isLoading: state.selectedLoadingIds.has(id),
      isFetching: state.selectedLoadingIds.has(id) || state.selectedFetchingIds.has(id),
      isError: state.selectedErrors.has(id),
      error: state.selectedErrors.get(id),
      refetch: state.selectedRefetch,
    };
  },
}));

vi.mock("./use-token-expiry-clock", () => ({
  useTokenExpiryClock: (tokens: Token[]) => {
    state.expiryClock(tokens);
    return state.clockNow;
  },
}));

function token(id: number, overrides: Partial<Token> = {}): Token {
  return {
    id,
    user_id: 7,
    key: `key-${id}`,
    name: `Token ${id}`,
    status: 1,
    expired_at: -1,
    models: "",
    trace_enabled: false,
    trace_mode: "full",
    created_at: 1,
    updated_at: 1,
    ...overrides,
  };
}

function bootstrap(tokens: Token[], total = tokens.length) {
  state.bootstrapData = { data: tokens, total, page: 1, page_size: 2 };
  for (const item of tokens) state.selectedTokens.set(item.id, item);
}

describe("useMarketplaceTokenSelection", () => {
  beforeEach(() => {
    window.localStorage.clear();
    state.query = "";
    state.bootstrapData = undefined;
    state.bootstrapLoading = false;
    state.bootstrapError = false;
    state.selectedTokens.clear();
    state.selectedLoadingIds.clear();
    state.selectedFetchingIds.clear();
    state.selectedErrors.clear();
    state.clockNow = 1_000;
    state.replace.mockReset();
    state.tokenList.mockReset();
    state.tokenListOptions.mockReset();
    state.tokenOne.mockReset();
    state.expiryClock.mockReset();
    state.refetch.mockReset();
    state.selectedRefetch.mockReset();
  });

  it("keeps an ordinary user unselected when no usable Token exists", async () => {
    bootstrap([], 0);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.ordinaryBootstrap.totalUsableTokens).toBe(0));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(result.current.ordinaryBootstrap.status).toBe("ready");
    expect(state.tokenList).toHaveBeenCalledWith({
      page: 1,
      page_size: 2,
      user_id: 7,
      usable_only: true,
    });
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("selects and remembers the only usable Token while atomically deleting page", async () => {
    const onlyToken = token(11);
    bootstrap([onlyToken], 1);
    state.query = "search=gpt&provider=OpenAI&kind=real&page=4&page_size=50";

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.selectedTokenId).toBe(11));
    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?search=gpt&provider=OpenAI&kind=real&page_size=50&token_id=11",
    );
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBe("11");
  });

  it("does not auto-select when multiple usable Tokens exist without a remembered choice", async () => {
    bootstrap([token(1), token(2)], 2);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.ordinaryBootstrap.totalUsableTokens).toBe(2));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("restores a remembered Token outside the two-row bootstrap page and validates it", async () => {
    bootstrap([token(1), token(2)], 8);
    const remembered = token(99);
    state.selectedTokens.set(99, remembered);
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "99");

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.selectedTokenId).toBe(99));
    expect(state.tokenOne).toHaveBeenCalledWith(99);
    expect(state.replace).toHaveBeenCalledWith("/model-marketplace?token_id=99");
  });

  it("does not restore another user's remembered Token", async () => {
    bootstrap([token(1), token(2)], 8);
    state.selectedTokens.set(99, token(99));
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "99");

    const { result } = renderHook(() => useMarketplaceTokenSelection(8, false));

    await waitFor(() => expect(result.current.ordinaryBootstrap.totalUsableTokens).toBe(8));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(state.tokenOne).not.toHaveBeenCalledWith(99);
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("exposes bootstrap failure and its refetch action", () => {
    state.bootstrapError = true;

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    expect(result.current.ordinaryBootstrap.status).toBe("error");
    expect(result.current.ordinaryBootstrap.retry).not.toBe(state.refetch);
    act(() => result.current.ordinaryBootstrap.retry());
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps a URL Token as the candidate while initial validation is pending", () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17";
    state.selectedLoadingIds.add(17);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    expect(result.current.candidateTokenId).toBe(17);
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(result.current.validation.status).toBe("initialPending");
    expect(result.current.tokenUnavailable).toBe(false);
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("keeps a validated cached Token selected during background validation", () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17";
    state.selectedTokens.set(17, token(17));
    state.selectedFetchingIds.add(17);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    expect(result.current.candidateTokenId).toBe(17);
    expect(result.current.selectedTokenId).toBe(17);
    expect(result.current.validation.status).toBe("backgroundFetching");
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("does not reactivate an old optimistic Token after navigation commits and goes back", () => {
    state.selectedTokens.set(17, token(17));
    const { result, rerender } = renderHook(() => useMarketplaceTokenSelection(7, true));

    act(() => result.current.handleTokenChange(17));
    expect(result.current.candidateTokenId).toBe(17);

    state.query = "token_id=17";
    rerender();
    expect(result.current.candidateTokenId).toBe(17);

    state.query = "";
    rerender();
    expect(result.current.candidateTokenId).toBeUndefined();
  });

  it("retains a candidate and exposes validation retry for a non-404 error", () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17";
    const error = new ApiError(500, "temporary failure");
    state.selectedErrors.set(17, error);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    expect(result.current.candidateTokenId).toBe(17);
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(result.current.validation).toMatchObject({
      status: "validationError",
      error,
    });
    expect(state.replace).not.toHaveBeenCalled();
    expect(state.refetch).not.toHaveBeenCalled();

    act(() => result.current.validation.retry());
    expect(state.selectedRefetch).toHaveBeenCalledTimes(1);
  });

  it("clears a deleted or foreign ordinary-user Token when validation returns 404", async () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17&search=gpt&page=3&page_size=50";
    state.selectedErrors.set(17, new ApiError(404, "not found"));
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "17");

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.tokenUnavailable).toBe(true));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBeNull();
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?search=gpt&page_size=50",
    );
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("does not reuse a stale Token response after the viewer changes", async () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17";
    state.selectedTokens.set(17, token(17, { user_id: 7 }));

    const { result, rerender } = renderHook(
      ({ userId }) => useMarketplaceTokenSelection(userId, false),
      { initialProps: { userId: 7 } },
    );
    expect(result.current.selectedTokenId).toBe(17);

    bootstrap([token(1, { user_id: 8 }), token(2, { user_id: 8 })], 2);
    rerender({ userId: 8 });

    await waitFor(() => expect(result.current.tokenUnavailable).toBe(true));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(state.replace).toHaveBeenCalledWith("/model-marketplace");
  });

  it("clears a disabled URL Token and makes repeated marketplace rejections idempotent", async () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17&search=gpt&page=3&page_size=20";
    state.selectedTokens.set(17, token(17, { status: 0 }));
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "17");

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.tokenUnavailable).toBe(true));
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBeNull();
    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?search=gpt&page_size=20",
    );
    expect(state.refetch).toHaveBeenCalledTimes(1);

    act(() => result.current.handleTokenUnavailable(17));
    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps expired_at equal to now valid and clears it on the next second", async () => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17&page=2";
    state.selectedTokens.set(17, token(17, { expired_at: 1_000 }));

    const { result, rerender } = renderHook(() => useMarketplaceTokenSelection(7, false));

    await waitFor(() => expect(result.current.selectedTokenId).toBe(17));
    expect(result.current.tokenUnavailable).toBe(false);
    expect(state.replace).not.toHaveBeenCalled();

    state.clockNow = 1_001;
    rerender();

    await waitFor(() => expect(result.current.selectedTokenId).toBeUndefined());
    expect(result.current.tokenUnavailable).toBe(true);
    expect(state.replace).toHaveBeenCalledWith("/model-marketplace");
  });

  it.each([
    ["disabled", { status: 0 }, { status: 1 }],
    ["extended expiry", { expired_at: 999 }, { expired_at: 2_000 }],
  ] as const)("allows a rejected Token to recover after %s and manual reselection", async (
    _name,
    rejectedOverrides,
    recoveredOverrides,
  ) => {
    bootstrap([token(1), token(2)], 2);
    state.query = "token_id=17";
    state.selectedTokens.set(17, token(17, rejectedOverrides));

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, false));
    await waitFor(() => expect(result.current.tokenUnavailable).toBe(true));

    state.selectedTokens.set(17, token(17, recoveredOverrides));
    act(() => result.current.handleTokenChange(17));

    await waitFor(() => expect(result.current.selectedTokenId).toBe(17));
    expect(result.current.tokenUnavailable).toBe(false);
  });

  it("keeps an administrator without token_id in global view", async () => {
    bootstrap([token(1)], 1);

    const { result } = renderHook(() => useMarketplaceTokenSelection(7, true));

    await waitFor(() => expect(result.current.ordinaryBootstrap.status).toBe("disabled"));
    expect(result.current.ordinaryBootstrap.totalUsableTokens).toBe(0);
    expect(result.current.selectedTokenId).toBeUndefined();
    expect(state.tokenListOptions).toHaveBeenCalledWith({ enabled: false });
    expect(state.replace).not.toHaveBeenCalled();
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBeNull();
  });
});
