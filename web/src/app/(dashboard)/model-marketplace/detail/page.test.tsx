import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AdminModelMarketplaceDetailResponse,
  MarketplaceModelOfferDetail,
  ModelMarketplaceDetailResponse,
} from "@/lib/api/model-marketplace";
import ModelMarketplaceDetailPage from "./page";

const state = vi.hoisted(() => ({
  query: "model=gpt-4o&token_id=17&window=24h",
  isAdmin: false,
  userId: 7,
  capability: true as boolean | undefined,
  capabilityLoading: false,
  capabilityError: false,
  userResponse: undefined as ModelMarketplaceDetailResponse | undefined,
  adminResponse: undefined as AdminModelMarketplaceDetailResponse | undefined,
  detailLoading: false,
  detailError: false,
  capabilityHook: vi.fn(),
  userDetail: vi.fn(),
  adminDetail: vi.fn(),
  refetch: vi.fn(),
  notFound: vi.fn(),
}));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(state.query),
  notFound: () => state.notFound(),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));

vi.mock("@/components/business/provider-avatar", () => ({
  ProviderAvatar: ({ provider, size }: { provider: string; size: number }) => (
    <svg aria-label={`${provider}-${size}`} />
  ),
}));

vi.mock("@/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    loading: false,
    isAdmin: state.isAdmin,
    user: { user_id: state.userId, role: state.isAdmin ? 2 : 1 },
  }),
}));

vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => {
    state.capabilityHook();
    return {
      data: state.capability === undefined ? undefined : { model_marketplace: state.capability },
      isLoading: state.capabilityLoading,
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
    useModelMarketplaceDetail: (params: unknown, viewerId: number) => {
      state.userDetail(params, viewerId);
      return {
        data: state.userResponse,
        isLoading: state.detailLoading,
        isError: state.detailError,
        refetch: state.refetch,
      };
    },
    useAdminModelMarketplaceDetail: (params: unknown, viewerId: number) => {
      state.adminDetail(params, viewerId);
      return {
        data: state.adminResponse,
        isLoading: state.detailLoading,
        isError: state.detailError,
        refetch: state.refetch,
      };
    },
  };
});

function offer(): MarketplaceModelOfferDetail {
  return {
    offer_ref: "offer-a",
    kind: "platform",
    display_name: "Platform A",
    ownership: "platform",
    available: true,
    supported_endpoints: ["responses", "models"],
    pricing: {
      reference_price: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      gateway_charge: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      estimated_total: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      accuracy: "exact",
    },
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
    },
    status_history: [],
    trend_series: [],
    usage_references: [],
  };
}

function realResponse(): ModelMarketplaceDetailResponse {
  return {
    selected_token: { id: 17, name: "Primary" },
    window: "24h",
    usage_status: "available",
    model: {
      kind: "real",
      real: {
        model_name: "gpt-4o",
        metadata: {
          display_name: "GPT-4o",
          description: "Production model",
          provider: "OpenAI",
          input_modalities: ["text"],
          output_modalities: ["text"],
          context_length: 128_000,
          max_output_tokens: 16_384,
          supported_parameters: ["tools"],
          tool_calling: true,
          structured_output: true,
          reasoning: false,
          prompt_cache: true,
        },
        aggregate_status: "operational",
        available_offer_count: 1,
        platform_offer_count: 1,
        private_offer_count: 0,
        offers: [offer()],
        performance: {
          performance_status: "available",
          window: "24h",
          success_rate: 99,
          cache_hit_rate: 0,
          status_history: [],
        },
      },
    },
  };
}

function routingResponse(): ModelMarketplaceDetailResponse {
  return {
    selected_token: { id: 17, name: "Primary" },
    window: "7d",
    usage_status: "not_applicable",
    model: {
      kind: "routing",
      routing: {
        model_name: "smart-route",
        display_name: "Smart route",
        reachable_real_models: ["gpt-4o", "claude-3-7"],
        flattened_destinations: [],
        routing_warnings: [],
        guidance: "view_reachable_real_models",
      },
    },
  };
}

function adminRealResponse(): AdminModelMarketplaceDetailResponse {
  const ordinary = realResponse();
  if (ordinary.model.kind !== "real") throw new Error("expected real model fixture");
  return {
    view: { mode: "global", selected_token: null },
    window: "30d",
    usage_status: "available",
    model: {
      kind: "real",
      real: {
        ...ordinary.model.real,
        offers: [{
          ...offer(),
          performance: {
            ...offer().performance,
            request_count: 101,
            success_count: 89,
            failure_count: 12,
            stream_request_count: 41,
            ttft_sample_count: 37,
            tps_sample_count: 31,
            duration_sample_count: 97,
          },
          diagnostics: {
            channel_id: 42,
            private_channel_id: 0,
            internal_name: "internal-source",
            public_display_name: "Public source",
            owner_id: 73,
            base_url: "https://diagnostics.example",
            endpoint_paths: [{ endpoint: "responses", path: "/v1/responses" }],
            disabled_reasons: ["disabled", "endpoints_not_configured"],
          },
        }],
      },
    },
  };
}

it("renders admin diagnostics when an older response encodes empty slices as null", () => {
  state.isAdmin = true;
  const response = adminRealResponse();
  if (response.model.kind !== "real") throw new Error("expected real model fixture");
  const diagnostics = response.model.real.offers[0].diagnostics as unknown as {
    endpoint_paths: null;
    disabled_reasons: null;
  };
  diagnostics.endpoint_paths = null;
  diagnostics.disabled_reasons = null;
  state.adminResponse = response;

  render(<ModelMarketplaceDetailPage />);

  expect(screen.getByText("GPT-4o")).toBeInTheDocument();
  expect(state.adminDetail).toHaveBeenCalled();
});

function adminRoutingResponse(): AdminModelMarketplaceDetailResponse {
  return {
    view: { mode: "global", selected_token: null },
    window: "7d",
    usage_status: "not_applicable",
    model: {
      kind: "routing",
      routing: {
        model_name: "smart-route",
        display_name: "Smart route",
        reachable_real_models: ["gpt-4o"],
        flattened_destinations: [],
        routing_warnings: [],
        guidance: "view_reachable_real_models",
        diagnostics: {
          definitions: [
            {
              occurrence_id: "root:91",
              path: [{ ref: "Primary route", routing_id: 91 }],
              routing_id: 91,
              name: "Primary route",
              scope: "global",
              user_id: 17,
              token_id: 23,
              enabled: true,
              members: [
                {
                  ref: "nested-route",
                  priority: 10,
                  weight: 3,
                  kind: "routing",
                  routing_id: 92,
                },
                {
                  ref: "gpt-4o",
                  priority: 5,
                  weight: 1,
                  kind: "model",
                  model_name: "gpt-4o",
                },
              ],
            },
            {
              occurrence_id: "root:91/0:92",
              path: [
                { ref: "Primary route", routing_id: 91 },
                { ref: "nested-route", routing_id: 92 },
              ],
              routing_id: 92,
              name: "nested-route",
              scope: "user",
              user_id: 29,
              token_id: 0,
              enabled: false,
              members: [{
                ref: "claude-3-7",
                priority: 4,
                weight: 2,
                kind: "model",
                model_name: "claude-3-7",
              }],
            },
            {
              occurrence_id: "root:93",
              path: [{ ref: "same", routing_id: 93 }],
              routing_id: 93,
              name: "same",
              scope: "global",
              user_id: 0,
              token_id: 0,
              enabled: true,
              members: [{
                ref: "same",
                priority: 1,
                weight: 1,
                kind: "model",
                model_name: "same",
              }],
            },
          ],
        },
      },
    },
  };
}

beforeEach(() => {
  window.history.replaceState(null, "", "/model-marketplace/detail?model=gpt-4o&token_id=17&window=24h");
  state.query = "model=gpt-4o&token_id=17&window=24h";
  state.isAdmin = false;
  state.userId = 7;
  state.capability = true;
  state.capabilityLoading = false;
  state.capabilityError = false;
  state.userResponse = realResponse();
  state.adminResponse = undefined;
  state.detailLoading = false;
  state.detailError = false;
  state.capabilityHook.mockReset();
  state.userDetail.mockReset();
  state.adminDetail.mockReset();
  state.refetch.mockReset();
  state.notFound.mockReset();
});

describe("model marketplace detail access and query boundary", () => {
  it("renders the real model as a compact header with shared token formatting", () => {
    render(<ModelMarketplaceDetailPage />);

    const header = screen.getByTestId("marketplace-model-header");
    expect(header).toHaveTextContent("GPT-4o");
    expect(within(header).getByText("128.00K")).toBeInTheDocument();
    expect(within(header).getByText("16.38K")).toBeInTheDocument();
    expect(header.closest('[data-slot="card"]')).toBeNull();
    expect(screen.queryByTestId("legacy-model-summary-card")).not.toBeInTheDocument();
  });

  it("models only the dense header, comparison, status matrix, and one chart while loading", () => {
    state.detailLoading = true;

    render(<ModelMarketplaceDetailPage />);

    const skeleton = screen.getByLabelText("loading");
    expect(within(skeleton).getAllByTestId("detail-skeleton-header")).toHaveLength(1);
    expect(within(skeleton).getAllByTestId("detail-skeleton-comparison")).toHaveLength(1);
    expect(within(skeleton).getAllByTestId("detail-skeleton-status")).toHaveLength(1);
    expect(within(skeleton).getAllByTestId("detail-skeleton-chart")).toHaveLength(1);
    expect(within(skeleton).queryByTestId("legacy-model-summary-card")).not.toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it.each([
    { loading: true, error: false, visible: undefined },
    { loading: false, error: true, visible: true },
    { loading: false, error: false, visible: false },
  ])("fails closed before detail data for $loading/$error/$visible", ({ loading, error, visible }) => {
    state.capabilityLoading = loading;
    state.capabilityError = error;
    state.capability = visible;

    render(<ModelMarketplaceDetailPage />);

    expect(state.capabilityHook).toHaveBeenCalled();
    expect(state.userDetail).not.toHaveBeenCalled();
    expect(state.adminDetail).not.toHaveBeenCalled();
    if (loading) expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    else expect(state.notFound).toHaveBeenCalled();
  });

  it("loads the ordinary detail with model, Token, window, and optional offer identity", () => {
    state.query = "model=gpt-4o&token_id=17&window=7d&offer_ref=offer-a";
    render(<ModelMarketplaceDetailPage />);

    expect(state.userDetail).toHaveBeenCalledWith({
      model: "gpt-4o",
      tokenId: 17,
      window: "7d",
      offerRef: "offer-a",
    }, 7);
    expect(state.adminDetail).not.toHaveBeenCalled();
    expect(screen.getByText("GPT-4o")).toBeInTheDocument();
  });

  it("lets an administrator query the independent global detail without a Token", () => {
    state.isAdmin = true;
    state.query = "model=gpt-4o&window=30d";
    state.adminResponse = adminRealResponse();

    render(<ModelMarketplaceDetailPage />);

    expect(state.capabilityHook).not.toHaveBeenCalled();
    expect(state.userDetail).not.toHaveBeenCalled();
    expect(state.adminDetail).toHaveBeenCalledWith({
      model: "gpt-4o",
      tokenId: undefined,
      window: "30d",
      offerRef: undefined,
    }, 7);
    expect(screen.getByText("adminGlobalView")).toBeInTheDocument();
  });

  it("projects every allowed administrator offer diagnostic without credentials", () => {
    state.isAdmin = true;
    state.query = "model=gpt-4o&window=30d";
    state.adminResponse = adminRealResponse();

    render(<ModelMarketplaceDetailPage />);

    expect(screen.getByText("adminDiagnosticsAdminOnly")).toBeInTheDocument();
    expect(screen.getByText(/Public source/)).toBeInTheDocument();
    expect(screen.getByText(/internal-source/)).toBeInTheDocument();
    expect(screen.getByText(/https:\/\/diagnostics\.example/)).toBeInTheDocument();
    expect(screen.getByText("adminChannelId").parentElement).toHaveTextContent("42");
    expect(screen.getByText("adminPrivateChannelId").parentElement).toHaveTextContent("0");
    expect(screen.getByText("adminOwnerId").parentElement).toHaveTextContent("73");
    expect(screen.getByText(/\/v1\/responses/)).toBeInTheDocument();
    expect(screen.getByText("disabledReason.disabled")).toBeInTheDocument();
    expect(screen.getByText("disabledReason.endpoints_not_configured")).toBeInTheDocument();
    expect(screen.getByText("adminRequestCount").parentElement).toHaveTextContent("101");
    expect(screen.getByText("adminSuccessCount").parentElement).toHaveTextContent("89");
    expect(screen.getByText("adminFailureCount").parentElement).toHaveTextContent("12");
    expect(screen.getByText("adminStreamRequestCount").parentElement).toHaveTextContent("41");
    expect(screen.getByText("adminTtftSampleCount").parentElement).toHaveTextContent("37");
    expect(screen.getByText("adminTpsSampleCount").parentElement).toHaveTextContent("31");
    expect(screen.getByText("adminDurationSampleCount").parentElement).toHaveTextContent("97");
    expect(screen.queryByText(/credential|cipher|secret/i)).not.toBeInTheDocument();
    expect(screen.queryByText("comparisonDescription")).not.toBeInTheDocument();
    expect(screen.queryByText("mobileOfferSummary")).not.toBeInTheDocument();
    expect(screen.queryByText("adminDiagnosticsDescription")).not.toBeInTheDocument();
  });

  it("does not inspect administrator-shaped extra fields on an ordinary response", () => {
    const ordinary = realResponse();
    if (ordinary.model.kind !== "real") throw new Error("expected real model fixture");
    const taintedOffer = ordinary.model.real.offers[0] as MarketplaceModelOfferDetail & {
      diagnostics: {
        internal_name: string;
        base_url: string;
        owner_id: number;
        channel_id: number;
      };
    };
    taintedOffer.diagnostics = {
      internal_name: "must-not-render",
      base_url: "https://must-not-render.example",
      owner_id: 73,
      channel_id: 42,
    };
    Object.assign(taintedOffer.performance, {
      request_count: 9_999,
      success_count: 9_998,
    });
    state.userResponse = ordinary;

    render(<ModelMarketplaceDetailPage />);

    expect(screen.queryByText(/must-not-render/)).not.toBeInTheDocument();
    expect(screen.queryByText(/9,999|9999/)).not.toBeInTheDocument();
    expect(screen.queryByText("adminDiagnosticsAdminOnly")).not.toBeInTheDocument();
    const accessibleLabels = Array.from(document.querySelectorAll("[aria-label]"))
      .map((node) => node.getAttribute("aria-label"))
      .join(" ");
    expect(accessibleLabels).not.toMatch(/must-not-render|owner.?73|channel.?42/i);
  });

  it("renders safe empty offer, status, trend, and usage collections without synthetic facts", () => {
    const response = realResponse();
    if (response.model.kind !== "real") throw new Error("expected real model fixture");
    response.model.real.offers = [];
    state.userResponse = response;

    render(<ModelMarketplaceDetailPage />);

    expect(screen.getByText("noOfferSelectedTitle")).toBeInTheDocument();
    expect(screen.queryByTestId("mobile-offer-fact-sheet")).not.toBeInTheDocument();
    expect(screen.queryByTestId("offer-status-row")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "trendTitle" })).not.toBeInTheDocument();
    expect(screen.getByText("usageEmptyTitle")).toBeInTheDocument();
  });

  it("keeps empty status, trend, and usage arrays as explicit no-observation states", () => {
    render(<ModelMarketplaceDetailPage />);

    const statusRow = screen.getByTestId("offer-status-row");
    expect(within(statusRow).getAllByRole("img")).toHaveLength(24);
    expect(within(statusRow).getAllByRole("img").every((cell) =>
      cell.getAttribute("data-state") === "unknown"
    )).toBe(true);
    expect(screen.getByText("trendEmpty")).toBeInTheDocument();
    expect(screen.getByText("usageEmptyTitle")).toBeInTheDocument();
  });

  it("uses the shared emerald outline badge for an operational model header", () => {
    render(<ModelMarketplaceDetailPage />);

    expect(within(screen.getByTestId("marketplace-model-header"))
      .getByText("status.operational")).toHaveClass("text-emerald-700");
  });

  it("renders the administrator routing definition and derived member chain", () => {
    state.isAdmin = true;
    state.query = "model=smart-route&window=7d";
    state.adminResponse = adminRoutingResponse();

    render(<ModelMarketplaceDetailPage />);

    const diagnostics = screen.getByRole("region", { name: "adminRoutingDiagnosticsTitle" });
    const primary = within(diagnostics).getByRole("article", { name: "Primary route" });
    expect(primary).toHaveTextContent("adminRoutingId: 91");
    expect(primary).toHaveTextContent("adminRoutingScope: global");
    expect(primary).toHaveTextContent("adminRoutingUserId: 17");
    expect(primary).toHaveTextContent("adminRoutingTokenId: 23");
    expect(primary).toHaveTextContent("adminRoutingEnabled: adminBoolean.true");

    const nestedMember = within(primary).getByRole("group", { name: "nested-route" });
    expect(nestedMember).toHaveTextContent("adminRoutingMemberType.routing");
    expect(nestedMember).toHaveTextContent("adminRoutingMemberName: nested-route");
    expect(nestedMember).toHaveTextContent("adminRoutingMemberRoutingId: 92");
    expect(nestedMember).toHaveTextContent("adminRoutingMemberPriority: 10");
    expect(nestedMember).toHaveTextContent("adminRoutingMemberWeight: 3");

    const modelMember = within(primary).getByRole("group", { name: "gpt-4o" });
    expect(modelMember).toHaveTextContent("adminRoutingMemberType.model");
    expect(modelMember).toHaveTextContent("adminRoutingMemberModelName: gpt-4o");
    expect(modelMember).toHaveTextContent("adminRoutingMemberPriority: 5");
    expect(modelMember).toHaveTextContent("adminRoutingMemberWeight: 1");

    const nested = within(diagnostics).getByRole("article", { name: "nested-route" });
    expect(nested).toHaveTextContent("adminRoutingId: 92");
    expect(nested).toHaveTextContent("adminRoutingEnabled: adminBoolean.false");
    expect(within(nested).getByRole("group", { name: "claude-3-7" }))
      .toHaveTextContent("adminRoutingMemberModelName: claude-3-7");

    const same = within(diagnostics).getByRole("article", { name: "same" });
    const sameNameModel = within(same).getByRole("group", { name: "same" });
    expect(sameNameModel).toHaveTextContent("adminRoutingMemberType.model");
    expect(sameNameModel).toHaveTextContent("adminRoutingMemberModelName: same");
    expect(sameNameModel).not.toHaveTextContent("adminRoutingMemberRoutingId");
    expect(diagnostics).not.toHaveTextContent(/credential|cipher|secret/i);
  });

  it("renders each same-routing occurrence with its own path context", () => {
    state.isAdmin = true;
    state.query = "model=smart-route&window=7d";
    const response = adminRoutingResponse();
    if (response.model.kind !== "routing") throw new Error("expected routing model fixture");
    const definitions = response.model.routing.diagnostics.definitions;
    definitions.push(
      {
        routing_id: 94,
        name: "shared",
        scope: "global",
        user_id: 0,
        token_id: 0,
        enabled: true,
        occurrence_id: "root:1/0:2/0:94",
        path: [
          { ref: "root", routing_id: 1 },
          { ref: "branch", routing_id: 2 },
          { ref: "shared", routing_id: 94 },
        ],
        members: [{ ref: "branch", priority: 0, weight: 1, kind: "model" }],
      },
      {
        routing_id: 94,
        name: "shared",
        scope: "global",
        user_id: 0,
        token_id: 0,
        enabled: true,
        occurrence_id: "root:1/1:3/0:94",
        path: [
          { ref: "root", routing_id: 1 },
          { ref: "other", routing_id: 3 },
          { ref: "shared", routing_id: 94 },
        ],
        members: [{ ref: "branch", priority: 0, weight: 1, kind: "routing" }],
      },
    );
    state.adminResponse = response;

    render(<ModelMarketplaceDetailPage />);

    const diagnostics = screen.getByRole("region", { name: "adminRoutingDiagnosticsTitle" });
    const shared = within(diagnostics).getAllByRole("article", { name: "shared" });
    expect(shared).toHaveLength(2);
    expect(shared[0]).toHaveTextContent("adminRoutingPath: root (1) → branch (2) → shared (94)");
    expect(shared[0]).toHaveTextContent("adminRoutingMemberType.model");
    expect(shared[1]).toHaveTextContent("adminRoutingPath: root (1) → other (3) → shared (94)");
    expect(shared[1]).toHaveTextContent("adminRoutingMemberType.routing");
  });

  it("does not issue an ordinary detail without a valid Token", () => {
    state.query = "model=gpt-4o&window=24h";
    render(<ModelMarketplaceDetailPage />);

    expect(state.userDetail).not.toHaveBeenCalled();
    expect(screen.getByText("detailTokenRequiredTitle")).toBeInTheDocument();
  });

  it("updates only the window through native history state", () => {
    const historySpy = vi.spyOn(window.history, "replaceState");
    state.query = "model=gpt-4o&token_id=17&window=24h&offer_ref=offer-a";
    render(<ModelMarketplaceDetailPage />);

    fireEvent.click(screen.getByRole("radio", { name: "window.30d" }));

    expect(historySpy).toHaveBeenLastCalledWith(
      null,
      "",
      "/model-marketplace/detail?model=gpt-4o&token_id=17&window=30d&offer_ref=offer-a",
    );
    historySpy.mockRestore();
  });

  it("renders routing guidance and reachable links without fabricated offer facts", () => {
    state.query = "model=smart-route&token_id=17&window=7d";
    state.userResponse = routingResponse();
    render(<ModelMarketplaceDetailPage />);

    expect(screen.getByText("routingDetailGuidance")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "gpt-4o" })).toHaveAttribute(
      "href",
      "/model-marketplace/detail?model=gpt-4o&token_id=17&window=7d",
    );
    expect(screen.queryByText("comparisonTitle")).not.toBeInTheDocument();
    expect(screen.queryByText("trendTitle")).not.toBeInTheDocument();
    expect(screen.queryByText("usageTitle")).not.toBeInTheDocument();
  });

  it("keeps the detail scope and retries a failed request", () => {
    state.detailError = true;
    render(<ModelMarketplaceDetailPage />);

    expect(screen.getByRole("alert")).toHaveTextContent("detailLoadErrorTitle");
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });
});
