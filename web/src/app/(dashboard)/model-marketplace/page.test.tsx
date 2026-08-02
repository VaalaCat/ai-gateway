import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AdminMarketplaceModelOfferDetail,
  MarketplaceModel,
  ModelMarketplaceListResponse,
} from "@/lib/api/model-marketplace";
import type { Token } from "@/lib/types";
import { ApiError } from "@/lib/api/client";
import ModelMarketplacePage from "./page";

function expectAdminOfferShape(
  offer: unknown,
): asserts offer is AdminMarketplaceModelOfferDetail {
  expect(offer).toEqual(expect.objectContaining({
    diagnostics: expect.objectContaining({
      internal_name: expect.any(String),
      base_url: expect.any(String),
      owner_id: expect.any(Number),
      channel_id: expect.any(Number),
      private_channel_id: expect.any(Number),
    }),
    performance: expect.objectContaining({
      request_count: expect.any(Number),
      success_count: expect.any(Number),
      failure_count: expect.any(Number),
      stream_request_count: expect.any(Number),
      ttft_sample_count: expect.any(Number),
      tps_sample_count: expect.any(Number),
      duration_sample_count: expect.any(Number),
    }),
  }));
}

const state = vi.hoisted(() => ({
  query: "",
  isAdmin: false,
  userId: 7,
  tokens: [] as Token[],
  tokensLoading: false,
  tokensError: false,
  capability: true as boolean | undefined,
  capabilityError: false,
  userResponse: undefined as ModelMarketplaceListResponse | undefined,
  adminResponse: undefined as {
    view: { mode: "global" | "token_preview"; selected_token: { id: number; name: string } | null };
    models: MarketplaceModel[];
    filters: { providers: string[]; input_modalities: string[]; output_modalities: string[] };
  } | undefined,
  marketplaceLoading: false,
  marketplaceError: undefined as unknown,
  replace: vi.fn(),
  notFound: vi.fn(),
  capabilityList: vi.fn(),
  tokenList: vi.fn(),
  tokenRefetch: vi.fn(),
  userList: vi.fn(),
  adminList: vi.fn(),
  refetch: vi.fn(),
}));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useSearchParams: () => new URLSearchParams(state.query),
  useRouter: () => ({
    replace: (href: string, options?: { scroll?: boolean }) => {
      state.replace(href, options);
    },
  }),
  notFound: () => state.notFound(),
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));
vi.mock("@/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));
vi.mock("@/components/business/provider-avatar", () => ({
  ProviderAvatar: ({ provider, size }: { provider: string; size: number }) => (
    <svg aria-label={`${provider}-${size}`} />
  ),
}));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    isAdmin: state.isAdmin,
    user: { user_id: state.userId, role: state.isAdmin ? 2 : 1 },
  }),
}));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: () => {
    state.tokenList();
    return {
    data: { data: state.tokens, total: state.tokens.length, page: 1, page_size: 100 },
    isLoading: state.tokensLoading,
    isError: state.tokensError,
    refetch: state.tokenRefetch,
  };
  },
  useMarketplaceTokens: () => {
    state.tokenList();
    return {
      data: state.tokens,
      isLoading: state.tokensLoading,
      isError: state.tokensError,
      refetch: state.tokenRefetch,
    };
  },
}));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => {
    state.capabilityList();
    return {
      data: state.capability === undefined ? undefined : { model_marketplace: state.capability },
      isLoading: false,
      isError: state.capabilityError,
    };
  },
  isModelMarketplaceVisible: (
    capabilities: { model_marketplace?: boolean } | undefined,
    isAdmin: boolean,
  ) => isAdmin || capabilities?.model_marketplace === true,
}));
vi.mock("@/lib/api/model-marketplace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/model-marketplace")>();
  return {
    ...actual,
    useMarketplaceTokens: () => {
      state.tokenList();
      return {
        data: state.tokens,
        isLoading: state.tokensLoading,
        isError: state.tokensError,
        refetch: state.tokenRefetch,
      };
    },
    useModelMarketplaceList: (params: unknown) => {
      state.userList(params);
      return {
        data: state.userResponse,
        isLoading: state.marketplaceLoading,
        isError: state.marketplaceError !== undefined,
        error: state.marketplaceError,
        refetch: state.refetch,
      };
    },
    useAdminModelMarketplaceList: (params: unknown) => {
      state.adminList(params);
      return {
        data: state.adminResponse,
        isLoading: state.marketplaceLoading,
        isError: state.marketplaceError !== undefined,
        error: state.marketplaceError,
        refetch: state.refetch,
      };
    },
  };
});

const replaceHistoryState = window.history.replaceState.bind(window.history);
const replaceHistoryStateSpy = vi.spyOn(window.history, "replaceState");

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

const realModel: MarketplaceModel = {
  kind: "real",
  real: {
    model_name: "gpt-4o",
    metadata: {
      display_name: "GPT-4o",
      description: "Multimodal production model",
      provider: "OpenAI",
      input_modalities: ["text", "image"],
      output_modalities: ["text"],
      context_length: 128000,
      max_output_tokens: 16384,
      supported_parameters: ["tools"],
      tool_calling: true,
      structured_output: true,
      reasoning: false,
      prompt_cache: true,
    },
    aggregate_status: "operational",
    available_offer_count: 2,
    platform_offer_count: 1,
    private_offer_count: 1,
    offers: [
      {
        offer_ref: "p:one",
        kind: "platform",
        display_name: "Platform",
        ownership: "platform",
        available: true,
        supported_endpoints: ["chat_completions", "responses"],
        pricing: {
          reference_price: { input: 1, output: 3, cache_read: 0, cache_write: 0 },
          gateway_charge: { input: 1, output: 3, cache_read: 0, cache_write: 0 },
          estimated_total: { input: 1, output: 3, cache_read: 0, cache_write: 0 },
          accuracy: "exact",
        },
        performance_status: "available",
        performance: { status: "operational", success_rate: null, ttft_avg_ms: null, ttft_p95_ms: null, tps_avg: null, tps_p5: null, duration_p95_ms: null, token_units: { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 } },
        status_history: [],
        trend_series: [],
        usage_references: [],
      },
      {
        offer_ref: "b:two",
        kind: "private",
        display_name: "Owned BYOK",
        ownership: "owned",
        available: true,
        supported_endpoints: ["responses"],
        pricing: {
          reference_price: { input: 2, output: 4, cache_read: 0, cache_write: 0 },
          gateway_charge: { input: 2, output: 4, cache_read: 0, cache_write: 0 },
          estimated_total: { input: 2, output: 4, cache_read: 0, cache_write: 0 },
          accuracy: "reference",
        },
        performance_status: "available",
        performance: { status: "operational", success_rate: null, ttft_avg_ms: null, ttft_p95_ms: null, tps_avg: null, tps_p5: null, duration_p95_ms: null, token_units: { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 } },
        status_history: [],
        trend_series: [],
        usage_references: [],
      },
    ],
    performance: {
      performance_status: "unavailable",
      window: "24h",
      success_rate: null,
      cache_hit_rate: null,
      status_history: Array.from({ length: 24 }, (_, index) => ({
        started_at: 1_752_494_400 + index * 3_600,
        ended_at: 1_752_498_000 + index * 3_600,
        status: "unknown" as const,
        in_progress: index === 23,
        success_rate: null,
      })),
    },
  },
};

const routingModel: MarketplaceModel = {
  kind: "routing",
  routing: {
    model_name: "smart-route",
    display_name: "Smart route",
    reachable_real_models: ["gpt-4o", "claude-3-7"],
    flattened_destinations: [],
    routing_warnings: [],
    guidance: "view_reachable_real_models",
  },
};

function userResponse(models: MarketplaceModel[] = [realModel]): ModelMarketplaceListResponse {
  return {
    selected_token: { id: 1, name: "Token 1" },
    models,
    filters: { providers: ["Anthropic", "OpenAI"], input_modalities: ["text"], output_modalities: ["text"] },
  };
}

function observeHistoryWrite(view: ReturnType<typeof render>, callIndex: number) {
  const href = replaceHistoryStateSpy.mock.calls[callIndex]?.[2];
  if (typeof href !== "string" && !(href instanceof URL)) {
    throw new Error(`missing replace call ${callIndex}`);
  }
  const target = new URL(href, window.location.origin);
  replaceHistoryState(null, "", target);
  state.query = target.searchParams.toString();
  view.rerender(<ModelMarketplacePage />);
}

function setCommittedSearch(view: ReturnType<typeof render>, search: string) {
  const target = new URL(window.location.href);
  target.search = search;
  replaceHistoryState(null, "", target);
  state.query = search;
  view.rerender(<ModelMarketplacePage />);
}

function userCatalogCallCount(tokenId: number) {
  return state.userList.mock.calls.filter(([params]) =>
    (params as { tokenId?: number }).tokenId === tokenId
  ).length;
}

beforeEach(() => {
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
  window.localStorage.clear();
  replaceHistoryState(null, "", "/model-marketplace");
  replaceHistoryStateSpy.mockClear();
  state.query = "";
  state.isAdmin = false;
  state.userId = 7;
  state.tokens = [];
  state.tokensLoading = false;
  state.tokensError = false;
  state.capability = true;
  state.capabilityError = false;
  state.userResponse = userResponse();
  state.adminResponse = {
    view: { mode: "global", selected_token: null },
    models: [realModel],
    filters: { providers: ["OpenAI"], input_modalities: ["text"], output_modalities: ["text"] },
  };
  state.marketplaceLoading = false;
  state.marketplaceError = undefined;
  state.replace.mockReset();
  state.notFound.mockReset();
  state.capabilityList.mockReset();
  state.tokenList.mockReset();
  state.tokenRefetch.mockReset();
  state.userList.mockReset();
  state.adminList.mockReset();
  state.refetch.mockReset();
});

describe("Token selection", () => {
  it("automatically selects the only valid Token and loads its catalog", async () => {
    state.tokens = [token(1)];
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
    expect(screen.getByRole("article", { name: "GPT-4o" })).toBeInTheDocument();
    expect(replaceHistoryStateSpy).toHaveBeenCalledWith(
      null,
      "",
      "/model-marketplace?token_id=1",
    );
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("uses a valid URL Token before the remembered Token when several are valid", async () => {
    state.tokens = [token(1), token(2)];
    state.query = "token_id=1";
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "2");
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
  });

  it("falls back to the last valid localStorage Token for multiple Tokens", async () => {
    state.tokens = [token(1), token(2)];
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "2");
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
  });

  it("requires an explicit choice when multiple Tokens have no valid URL or remembered value", async () => {
    state.tokens = [token(1), token(2)];
    state.query = "token_id=999";
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(screen.getByText("chooseTokenTitle")).toBeInTheDocument());
    expect(state.userList).not.toHaveBeenCalled();
    expect(screen.queryByRole("article", { name: "GPT-4o" })).not.toBeInTheDocument();
  });

  it("shows the Token creation path when no enabled unexpired Token exists", () => {
    const expired = Math.floor(Date.now() / 1000) - 60;
    state.tokens = [token(1, { status: 0 }), token(2, { expired_at: expired })];
    render(<ModelMarketplacePage />);

    expect(screen.getByText("noTokenTitle")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "manageTokens" })).toHaveAttribute("href", "/tokens");
  });

  it("switches the URL and immediately issues a new request for the chosen Token", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2)];
    state.query = "token_id=1";
    render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));

    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
    expect(replaceHistoryStateSpy).toHaveBeenLastCalledWith(
      null,
      "",
      "/model-marketplace?token_id=2",
    );
    expect(state.replace).not.toHaveBeenCalled();
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBe("2");
  });

  it("replaces only token_id in the current static-export URL without a router flight", async () => {
    const user = userEvent.setup();
    replaceHistoryState(
      null,
      "",
      "/gateway/model-marketplace/?search=gpt&provider=OpenAI&token_id=1#offers",
    );
    state.tokens = [token(1), token(2)];
    state.query = "search=gpt&provider=OpenAI&token_id=1";
    render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));

    expect(replaceHistoryStateSpy).toHaveBeenLastCalledWith(
      null,
      "",
      "/gateway/model-marketplace/?search=gpt&provider=OpenAI&token_id=2#offers",
    );
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("settles optimistic selection on URL commit and treats later Back/Forward URLs as truth", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2), token(3)];
    state.query = "token_id=1";
    const view = render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));

    observeHistoryWrite(view, 0);
    setCommittedSearch(view, "token_id=1");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));

    setCommittedSearch(view, "token_id=2");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
  });

  it("settles two synchronous history replacements in issue order", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2), token(3)];
    state.query = "token_id=1";
    const view = render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
    observeHistoryWrite(view, 0);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 3" }));
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 3 }),
    ));
    const tokenTwoCalls = userCatalogCallCount(2);

    observeHistoryWrite(view, 1);
    expect(userCatalogCallCount(2)).toBe(tokenTwoCalls);
    expect(window.location.search).toBe("?token_id=3");
    expect(state.replace).not.toHaveBeenCalled();
    setCommittedSearch(view, "token_id=1");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
  });

  it("eliminates the async router flight that could commit latest then superseded", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2), token(3)];
    state.query = "token_id=1";
    const view = render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));
    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 3" }));
    const tokenTwoCalls = userCatalogCallCount(2);

    expect(replaceHistoryStateSpy.mock.calls.map(([, , href]) => href)).toEqual([
      "/model-marketplace?token_id=2",
      "/model-marketplace?token_id=3",
    ]);
    expect(window.location.search).toBe("?token_id=3");
    expect(state.replace).not.toHaveBeenCalled();

    observeHistoryWrite(view, 1);
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 3 }),
    ));
    expect(userCatalogCallCount(2)).toBe(tokenTwoCalls);
    setCommittedSearch(view, "token_id=1");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
  });

  it("settles when a superseded navigation is canceled before it can commit", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2), token(3)];
    state.query = "token_id=1";
    const view = render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));
    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 3" }));

    observeHistoryWrite(view, 1);
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 3 }),
    ));

    setCommittedSearch(view, "token_id=1");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
    setCommittedSearch(view, "token_id=2");
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
  });

  it("keeps admin global despite a sole Token and remembered ordinary-user selection", async () => {
    state.isAdmin = true;
    state.tokens = [token(1)];
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "1");
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.adminList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: undefined }),
    ));
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("adminGlobalOption");
  });

  it("lets admin explicitly preview a Token and return to global by removing token_id", async () => {
    const user = userEvent.setup();
    state.isAdmin = true;
    state.tokens = [token(1)];
    state.query = "token_id=1";
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.adminList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1 }),
    ));
    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "adminGlobalOption" }));

    await waitFor(() => expect(state.adminList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: undefined }),
    ));
    expect(replaceHistoryStateSpy).toHaveBeenLastCalledWith(null, "", "/model-marketplace");
    expect(state.replace).not.toHaveBeenCalled();
  });

  it("recomputes Token validity after its exact backend expiry boundary", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-01T00:00:00.000Z"));
    try {
      const now = Math.floor(Date.now() / 1000);
      state.tokens = [token(1, { expired_at: now })];
      state.query = "token_id=1";
      render(<ModelMarketplacePage />);

      expect(state.userList).toHaveBeenCalledWith(expect.objectContaining({ tokenId: 1 }));
      await act(async () => { await vi.advanceTimersByTimeAsync(1_001); });
      expect(screen.getByText("noTokenTitle")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("uses the absolute millisecond boundary when mounted between Unix seconds", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-01T00:00:00.100Z"));
    try {
      const expiresAt = Math.floor(Date.now() / 1000);
      state.tokens = [token(1, { expired_at: expiresAt })];
      state.query = "token_id=1";
      render(<ModelMarketplacePage />);

      await act(async () => { await vi.advanceTimersByTimeAsync(899); });
      expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("Token 1");
      await act(async () => { await vi.advanceTimersByTimeAsync(2); });
      expect(screen.getByText("noTokenTitle")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops an optimistic Token as soon as it expires while navigation is pending", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-01T00:00:00.100Z"));
    try {
      const expiresAt = Math.floor(Date.now() / 1000);
      state.tokens = [token(1), token(2, { expired_at: expiresAt })];
      state.query = "token_id=1";
      render(<ModelMarketplacePage />);

      fireEvent.click(screen.getByRole("combobox", { name: "tokenLabel" }));
      fireEvent.click(screen.getByRole("option", { name: "Token 2" }));
      expect(state.userList).toHaveBeenLastCalledWith(expect.objectContaining({ tokenId: 2 }));
      const tokenTwoCalls = userCatalogCallCount(2);

      await act(async () => { await vi.advanceTimersByTimeAsync(901); });

      expect(userCatalogCallCount(2)).toBe(tokenTwoCalls);
      expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("Token 1");
      expect(state.userList).toHaveBeenLastCalledWith(expect.objectContaining({ tokenId: 1 }));
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops an optimistic Token when a refreshed Token list disables it", async () => {
    const user = userEvent.setup();
    state.tokens = [token(1), token(2)];
    state.query = "token_id=1";
    const view = render(<ModelMarketplacePage />);

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(screen.getByRole("option", { name: "Token 2" }));
    const tokenTwoCalls = userCatalogCallCount(2);
    state.tokens = [token(1), token(2, { status: 0 })];
    view.rerender(<ModelMarketplacePage />);

    expect(userCatalogCallCount(2)).toBe(tokenTwoCalls);
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("Token 1");
    expect(state.userList).toHaveBeenLastCalledWith(expect.objectContaining({ tokenId: 1 }));
  });

  it.each([30, 60])("segments a %d-day Token timer without overflow or hot-looping", async (days) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-01T00:00:00.100Z"));
    const timerSpy = vi.spyOn(window, "setTimeout");
    const maxDelay = 2_147_483_647;
    try {
      const expiresAt = Math.floor(Date.now() / 1000) + days * 86_400;
      const deadline = (expiresAt + 1) * 1_000;
      state.tokens = [token(1, { expired_at: expiresAt })];
      state.query = "token_id=1";
      render(<ModelMarketplacePage />);

      const expiryDelays = () => timerSpy.mock.calls
        .map(([, delay]) => Number(delay))
        .filter((delay) => delay >= 86_400_000);
      expect(expiryDelays()[0]).toBe(maxDelay);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(expiryDelays()).toHaveLength(1);

      while (deadline - Date.now() > maxDelay) {
        await act(async () => { await vi.advanceTimersByTimeAsync(maxDelay); });
        expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("Token 1");
      }
      const finalDelay = deadline - Date.now();
      await act(async () => { await vi.advanceTimersByTimeAsync(finalDelay - 1); });
      expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("Token 1");
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("noTokenTitle")).toBeInTheDocument();
    } finally {
      timerSpy.mockRestore();
      vi.useRealTimers();
    }
  });

  it("recomputes Token validity when the document becomes visible again", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-01T00:00:00.000Z"));
    try {
      const now = Math.floor(Date.now() / 1000);
      state.tokens = [token(1, { expired_at: now + 10 })];
      state.query = "token_id=1";
      render(<ModelMarketplacePage />);

      vi.setSystemTime(new Date("2026-08-01T00:00:20.000Z"));
      act(() => document.dispatchEvent(new Event("visibilitychange")));
      expect(screen.getByText("noTokenTitle")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("capability and Token failures", () => {
  it.each([
    { capability: false as boolean | undefined, capabilityError: false },
    { capability: undefined as boolean | undefined, capabilityError: false },
    { capability: undefined as boolean | undefined, capabilityError: true },
  ])("fails closed before mounting private hooks for $capability/$capabilityError", (capabilityState) => {
    state.capability = capabilityState.capability;
    state.capabilityError = capabilityState.capabilityError;
    render(<ModelMarketplacePage />);

    expect(state.notFound).toHaveBeenCalledTimes(1);
    expect(state.tokenList).not.toHaveBeenCalled();
    expect(state.userList).not.toHaveBeenCalled();
    expect(state.adminList).not.toHaveBeenCalled();
  });

  it("bypasses the capability query result for administrators", async () => {
    state.isAdmin = true;
    state.capability = false;
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.adminList).toHaveBeenCalled());
    expect(state.notFound).not.toHaveBeenCalled();
    expect(state.capabilityList).not.toHaveBeenCalled();
  });

  it("shows Token-list failure as an actionable state and recovers after retry", () => {
    state.tokensError = true;
    const view = render(<ModelMarketplacePage />);

    expect(screen.getByRole("alert")).toHaveTextContent("tokenLoadErrorTitle");
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(state.tokenRefetch).toHaveBeenCalledTimes(1);
    expect(state.userList).not.toHaveBeenCalled();
    expect(state.adminList).not.toHaveBeenCalled();
    expect(screen.queryByText("noTokenTitle")).not.toBeInTheDocument();

    state.tokensError = false;
    state.tokens = [token(1)];
    view.rerender(<ModelMarketplacePage />);
    expect(state.userList).toHaveBeenCalledWith(expect.objectContaining({ tokenId: 1 }));
  });

  it("refreshes Tokens and clears a server-rejected selection instead of generically retrying 422", async () => {
    state.tokens = [token(1), token(2)];
    state.query = "token_id=1";
    state.marketplaceError = new ApiError(422, "expired", { code: "marketplace_token_expired" });
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.tokenRefetch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("tokenUnavailableTitle"));
    expect(replaceHistoryStateSpy).toHaveBeenLastCalledWith(null, "", "/model-marketplace");
    expect(state.replace).not.toHaveBeenCalled();
    expect(screen.getByText("chooseTokenTitle")).toBeInTheDocument();

    state.marketplaceError = undefined;
    await userEvent.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await userEvent.click(screen.getByRole("option", { name: "Token 2" }));
    await waitFor(() => expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 2 }),
    ));
  });
});

describe("catalog requests and states", () => {
  it("uses the administrator hook for an always-available global catalog", async () => {
    state.isAdmin = true;
    render(<ModelMarketplacePage />);

    await waitFor(() => expect(state.adminList).toHaveBeenCalledWith(
      expect.objectContaining({ tokenId: undefined }),
    ));
    expect(state.userList).not.toHaveBeenCalled();
    expect(screen.getByRole("article", { name: "GPT-4o" })).toBeInTheDocument();
  });

  it("renders distinct real supply fingerprints and routing guidance without fabricated metrics", async () => {
    state.tokens = [token(1)];
    state.query = "token_id=1";
    state.userResponse = userResponse([realModel, routingModel]);
    render(<ModelMarketplacePage />);

    expect(screen.getByTestId("marketplace-directory")).toHaveClass(
      "min-w-0",
      "overflow-x-auto",
    );
    const catalog = screen.getByTestId("model-catalog-list");
    expect(catalog).toHaveAttribute("data-slot", "card");
    expect(within(catalog).getAllByRole("article")).toHaveLength(2);
    expect(within(catalog).getAllByTestId("model-catalog-separator")).toHaveLength(1);

    const real = screen.getByRole("article", { name: "GPT-4o" });
    expect(real).not.toHaveAttribute("data-slot", "card");
    expect(within(real).getByTestId("marketplace-model-identity")).toBeInTheDocument();
    const performanceStrip = within(real).getByTestId("model-performance-strip");
    expect(performanceStrip).toHaveTextContent("performanceWindow24h");
    expect(performanceStrip).toHaveTextContent("—");
    expect(performanceStrip).not.toHaveTextContent("%");
    const unknownBlocks = within(performanceStrip).getAllByRole("img");
    expect(unknownBlocks).toHaveLength(24);
    for (const block of unknownBlocks) {
      expect(block).toHaveClass("bg-gray-400");
      expect(block).toHaveAccessibleName(/status\.unknown/);
    }
    expect(within(real).queryByTestId("offer-availability-tracker")).not.toBeInTheDocument();
    expect(within(real).getByLabelText("channelGroupLabel")).toBeInTheDocument();
    expect(within(real).getByText("endpoint.chat_completions")).toBeInTheDocument();
    expect(within(real).queryByText("status.operational")).not.toBeInTheDocument();
    const prices = within(real).getAllByTestId("marketplace-price-item");
    expect(prices.map((item) => item.getAttribute("data-price-key"))).toEqual([
      "input",
      "cache_read",
      "output",
      "cache_write",
    ]);
    expect(prices[0]).toHaveTextContent("$1.00 – $2.00");
    expect(prices[2]).toHaveTextContent("$3.00 – $4.00");

    const routing = screen.getByRole("article", { name: "Smart route" });
    expect(within(routing).getByText("viewReachableRealModels")).toBeInTheDocument();
    expect(within(routing).getByText("gpt-4o")).toBeInTheDocument();
    expect(within(routing).queryByTestId("marketplace-price-item")).not.toBeInTheDocument();
    expect(within(routing).queryByTestId("offer-availability-tracker")).not.toBeInTheDocument();
    expect(within(routing).queryByTestId("model-performance-strip")).not.toBeInTheDocument();
  });

  it("keeps ordinary aria labels and the channel popover limited to public offer fields", async () => {
    const user = userEvent.setup();
    const tainted = structuredClone(realModel);
    if (tainted.kind !== "real") throw new Error("expected real model fixture");
    tainted.real.offers = Array.from({ length: 6 }, (_, index) => {
      const adminOffer: AdminMarketplaceModelOfferDetail = {
        ...tainted.real.offers[0],
        offer_ref: `public-${index}`,
        display_name: `Public channel ${index + 1}`,
        performance_status: "available",
        performance: {
          status: "operational",
          success_rate: 99,
          ttft_avg_ms: 100,
          ttft_p95_ms: 120,
          tps_avg: 40,
          tps_p5: 20,
          duration_p95_ms: 1_000,
          token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
          request_count: 10_000 + index,
          success_count: 9_000 + index,
          failure_count: 1_000 + index,
          stream_request_count: 8_000 + index,
          ttft_sample_count: 7_000 + index,
          tps_sample_count: 6_000 + index,
          duration_sample_count: 5_000 + index,
        },
        status_history: [],
        trend_series: [],
        usage_references: [],
        diagnostics: {
          internal_name: `secret-internal-${index}`,
          base_url: `https://must-not-leak-${index}.example`,
          public_display_name: `Public channel ${index + 1}`,
          owner_id: 700 + index,
          channel_id: 400 + index,
          private_channel_id: 900 + index,
          endpoint_paths: [{ endpoint: "responses", path: `/secret-path-${index}` }],
          disabled_reasons: [`secret-disabled-${index}`],
        },
      };
      return adminOffer;
    });
    expectAdminOfferShape(tainted.real.offers[0]);
    state.tokens = [token(1)];
    state.query = "token_id=1";
    state.userResponse = userResponse([tainted]);

    render(<ModelMarketplacePage />);

    const secretPattern = /secret-internal|must-not-leak|secret-path|secret-disabled|70[0-5]|40[0-5]|90[0-5]/i;
    const accessibleLabels = Array.from(document.querySelectorAll("[aria-label]"))
      .map((node) => node.getAttribute("aria-label"))
      .join(" ");
    expect(accessibleLabels).not.toMatch(secretPattern);

    await user.click(screen.getByRole("button", { name: "showAllChannels 6" }));
    const popover = await screen.findByRole("dialog", { name: "allChannelsDialogLabel 6" });
    expect(popover).toHaveTextContent("Public channel 6");
    expect(popover).not.toHaveTextContent(secretPattern);
    expect(popover).not.toHaveAccessibleName(secretPattern);
    const mountedAccessibleLabels = Array.from(document.querySelectorAll("[aria-label]"))
      .map((node) => node.getAttribute("aria-label"))
      .join(" ");
    expect(mountedAccessibleLabels).not.toMatch(secretPattern);
  });

  it("shows a catalog skeleton while the selected Token request is loading", () => {
    state.tokens = [token(1)];
    state.query = "token_id=1";
    state.marketplaceLoading = true;
    render(<ModelMarketplacePage />);

    const skeleton = screen.getByLabelText("catalogLoading");
    expect(skeleton).toHaveAttribute("aria-busy", "true");
    expect(within(skeleton).getAllByTestId("catalog-skeleton-row")).toHaveLength(4);
    expect(within(skeleton).getAllByTestId("catalog-skeleton-identity")).toHaveLength(4);
    expect(within(skeleton).getAllByTestId("catalog-skeleton-availability")).toHaveLength(4);
    expect(within(skeleton).getAllByTestId("catalog-skeleton-prices")).toHaveLength(4);
    expect(within(skeleton).getAllByTestId("catalog-skeleton-channels")).toHaveLength(4);
    for (const row of within(skeleton).getAllByTestId("catalog-skeleton-row")) {
      expect(Array.from(row.children).map((child) => child.getAttribute("data-testid")))
        .toEqual([
          "catalog-skeleton-identity",
          "catalog-skeleton-availability",
          "catalog-skeleton-prices",
          "catalog-skeleton-channels",
        ]);
    }
  });

  it("shows an actionable error and retries the failed request", () => {
    state.tokens = [token(1)];
    state.query = "token_id=1";
    state.marketplaceError = true;
    render(<ModelMarketplacePage />);

    expect(screen.getByRole("alert")).toHaveTextContent("loadErrorTitle");
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("distinguishes a successful empty catalog from an error", () => {
    state.tokens = [token(1)];
    state.query = "token_id=1";
    state.userResponse = userResponse([]);
    render(<ModelMarketplacePage />);

    expect(screen.getByText("emptyTitle")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
