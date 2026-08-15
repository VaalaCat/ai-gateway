import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AdminModelMarketplaceListResponse,
  MarketplaceModel,
  ModelMarketplaceListResponse,
} from "@/lib/api/model-marketplace";
import type { FilterSpec, FilterValues } from "@/components/data-table/filter-spec";
import type { MarketplaceTokenSelection } from "@/components/model-marketplace/use-marketplace-token-selection";
import ModelMarketplacePage from "./page";

interface ToolbarProps {
  spec: FilterSpec;
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
}

interface PaginationProps {
  page: number;
  pageSize: number;
  pageCount: number;
  onPaginationChange: (page: number, pageSize: number) => void;
}

const state = vi.hoisted(() => ({
  query: "token_id=1",
  isAdmin: false,
  capability: true,
  capabilityLoading: false,
  capabilityError: false,
  selectedTokenId: 1 as number | undefined,
  candidateTokenId: 1 as number | undefined,
  totalUsableTokens: 2,
  tokenLoading: false,
  tokenError: false,
  tokenUnavailable: false,
  userResponse: undefined as ModelMarketplaceListResponse | undefined,
  adminResponse: undefined as AdminModelMarketplaceListResponse | undefined,
  marketplaceLoading: false,
  marketplaceFetching: false,
  marketplaceError: undefined as unknown,
  replace: vi.fn(),
  notFound: vi.fn(),
  handleTokenChange: vi.fn(),
  handleTokenUnavailable: vi.fn(),
  tokenRefetch: vi.fn(),
  catalogRefetch: vi.fn(),
  userList: vi.fn(),
  adminList: vi.fn(),
  toolbarProps: undefined as ToolbarProps | undefined,
  paginationProps: undefined as PaginationProps | undefined,
}));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useSearchParams: () => new URLSearchParams(state.query),
  useRouter: () => ({ replace: state.replace }),
  notFound: () => state.notFound(),
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => false }));
vi.mock("@/components/model-marketplace/model-card", () => ({
  ModelCard: ({ model }: { model: MarketplaceModel }) => {
    const name = model.kind === "real" ? model.real.metadata.display_name : model.routing.display_name;
    return <article aria-label={name}>{name}</article>;
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: (props: ToolbarProps) => {
    state.toolbarProps = props;
    return <div data-testid="standard-toolbar" />;
  },
}));
vi.mock("@/components/data-table/pagination", () => ({
  DataTablePagination: (props: PaginationProps) => {
    state.paginationProps = props;
    return <nav aria-label="shared-pagination">{props.page}/{props.pageCount}</nav>;
  },
}));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    loading: false,
    isAdmin: state.isAdmin,
    user: { user_id: 7, role: state.isAdmin ? 2 : 1 },
  }),
}));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({
    data: { model_marketplace: state.capability },
    isLoading: state.capabilityLoading,
    isError: state.capabilityError,
  }),
  isModelMarketplaceVisible: () => state.capability,
}));
vi.mock("@/components/model-marketplace/use-marketplace-token-selection", () => ({
  useMarketplaceTokenSelection: (): MarketplaceTokenSelection => ({
    candidateTokenId: state.candidateTokenId,
    selectedTokenId: state.selectedTokenId,
    validation: {
      status: "validated",
      retry: state.tokenRefetch,
    },
    ordinaryBootstrap: {
      status: state.isAdmin ? "disabled" : state.tokenLoading
        ? "initialPending"
        : state.tokenError
          ? "error"
          : "ready",
      totalUsableTokens: state.totalUsableTokens,
      retry: state.tokenRefetch,
    },
    tokenUnavailable: state.tokenUnavailable,
    handleTokenChange: state.handleTokenChange,
    handleTokenUnavailable: state.handleTokenUnavailable,
  }),
}));
vi.mock("@/lib/api/model-marketplace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/model-marketplace")>();
  return {
    ...actual,
    useModelMarketplaceList: (params: unknown, viewerId: number, options: unknown) => {
      state.userList(params, viewerId, options);
      return {
        data: state.userResponse,
        isLoading: state.marketplaceLoading,
        isFetching: state.marketplaceFetching,
        isError: state.marketplaceError !== undefined,
        error: state.marketplaceError,
        refetch: state.catalogRefetch,
      };
    },
    useAdminModelMarketplaceList: (params: unknown, viewerId: number, options: unknown) => {
      state.adminList(params, viewerId, options);
      return {
        data: state.adminResponse,
        isLoading: state.marketplaceLoading,
        isFetching: state.marketplaceFetching,
        isError: state.marketplaceError !== undefined,
        error: state.marketplaceError,
        refetch: state.catalogRefetch,
      };
    },
  };
});

function realModel(name = "GPT-4o"): MarketplaceModel {
  return {
    kind: "real",
    real: {
      model_name: name.toLowerCase(),
      metadata: {
        display_name: name,
        description: "",
        provider: "OpenAI",
        input_modalities: ["text"],
        output_modalities: ["text"],
        context_length: 1,
        max_output_tokens: 1,
        supported_parameters: [],
        tool_calling: false,
        structured_output: false,
        reasoning: false,
        prompt_cache: false,
      },
      aggregate_status: "operational",
      available_offer_count: 0,
      platform_offer_count: 0,
      private_offer_count: 0,
      offers: [],
      performance: {
        performance_status: "unavailable",
        window: "24h",
        success_rate: null,
        cache_hit_rate: null,
        status_history: [],
      },
    },
  };
}

function userResponse(overrides: Partial<ModelMarketplaceListResponse> = {}): ModelMarketplaceListResponse {
  return {
    selected_token: { id: 1, name: "Primary" },
    models: [realModel()],
    filters: {
      providers: ["Anthropic", "OpenAI"],
      input_modalities: ["text"],
      output_modalities: ["text"],
    },
    total: 41,
    page: 1,
    page_size: 20,
    ...overrides,
  };
}

function adminResponse(
  overrides: Partial<AdminModelMarketplaceListResponse> = {},
): AdminModelMarketplaceListResponse {
  return {
    view: { mode: "global", selected_token: null },
    models: [realModel()],
    filters: {
      providers: ["OpenAI"],
      input_modalities: ["text"],
      output_modalities: ["text"],
    },
    total: 1,
    page: 1,
    page_size: 20,
    ...overrides,
  };
}

beforeEach(() => {
  state.query = "token_id=1";
  state.isAdmin = false;
  state.capability = true;
  state.capabilityLoading = false;
  state.capabilityError = false;
  state.selectedTokenId = 1;
  state.candidateTokenId = 1;
  state.totalUsableTokens = 2;
  state.tokenLoading = false;
  state.tokenError = false;
  state.tokenUnavailable = false;
  state.userResponse = userResponse();
  state.adminResponse = adminResponse();
  state.marketplaceLoading = false;
  state.marketplaceFetching = false;
  state.marketplaceError = undefined;
  state.replace.mockReset();
  state.notFound.mockReset();
  state.handleTokenChange.mockReset();
  state.handleTokenUnavailable.mockReset();
  state.tokenRefetch.mockReset();
  state.catalogRefetch.mockReset();
  state.userList.mockReset();
  state.adminList.mockReset();
  state.toolbarProps = undefined;
  state.paginationProps = undefined;
});

describe("marketplace catalog controls", () => {
  it("keeps exactly one page heading while the ordinary catalog capability is loading", () => {
    state.capabilityLoading = true;

    render(<ModelMarketplacePage />);

    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("renders the standard toolbar with usable Token, search, provider and kind", () => {
    render(<ModelMarketplacePage />);

    expect(screen.getByTestId("standard-toolbar")).toBeInTheDocument();
    expect(Object.keys(state.toolbarProps?.spec ?? {})).toEqual([
      "token_id",
      "search",
      "provider",
      "kind",
    ]);
    expect(state.toolbarProps?.spec.token_id).toMatchObject({
      kind: "picker",
      entity: "usable-token",
      placeholder: "tokenPlaceholder",
    });
    expect(state.toolbarProps?.spec.search).toMatchObject({
      kind: "text",
      debounceMs: 300,
    });
    expect(state.toolbarProps?.spec.provider).toMatchObject({
      options: [
        { value: "Anthropic", label: "Anthropic" },
        { value: "OpenAI", label: "OpenAI" },
      ],
    });
  });

  it("requests page 2 with page size 20 and renders the shared pagination", () => {
    state.query = "token_id=1&page=2";
    state.userResponse = userResponse({ page: 2 });

    render(<ModelMarketplacePage />);

    expect(state.userList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: 1, page: 2, pageSize: 20 }),
      7,
      expect.any(Object),
    );
    expect(screen.getByRole("navigation", { name: "shared-pagination" })).toHaveTextContent("2/3");
    expect(state.paginationProps).toMatchObject({ page: 2, pageSize: 20, pageCount: 3 });
  });

  it("resets page when Token or a catalog filter changes", () => {
    state.query = "token_id=1&search=old&page=4&page_size=50";
    render(<ModelMarketplacePage />);

    act(() => state.toolbarProps?.onChange({ token_id: "9" }));
    expect(state.handleTokenChange).toHaveBeenCalledWith(9);

    act(() => state.toolbarProps?.onChange({ provider: "OpenAI" }));
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=old&page_size=50&provider=OpenAI",
    );

    act(() => state.paginationProps?.onPaginationChange(3, 10));
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=old&page_size=10&provider=OpenAI&page=3",
    );
  });

  it("keeps admin clear Token as global view", () => {
    state.isAdmin = true;
    state.query = "";
    state.selectedTokenId = undefined;
    state.candidateTokenId = undefined;

    render(<ModelMarketplacePage />);

    expect(state.adminList).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: undefined, page: 1, pageSize: 20 }),
      7,
      expect.any(Object),
    );
    expect(state.toolbarProps?.value.token_id).toBe("");
    expect(state.toolbarProps?.spec.token_id).toMatchObject({
      entity: "usable-token",
      placeholder: "adminGlobalOption",
    });
    expect(screen.getByText("adminGlobalView")).toBeInTheDocument();
  });

  it("shows no-token and choose-token states below the same toolbar", () => {
    state.query = "";
    state.selectedTokenId = undefined;
    state.candidateTokenId = undefined;
    state.totalUsableTokens = 0;
    const view = render(<ModelMarketplacePage />);

    expect(screen.getByTestId("standard-toolbar")).toBeInTheDocument();
    expect(screen.getByText("noTokenTitle")).toBeInTheDocument();

    state.totalUsableTokens = 2;
    view.rerender(<ModelMarketplacePage />);
    expect(screen.getByTestId("standard-toolbar")).toBeInTheDocument();
    expect(screen.getByText("chooseTokenTitle")).toBeInTheDocument();
  });

  it("keeps an over-bound page empty and uses the server total for pagination", () => {
    state.query = "token_id=1&page=9";
    state.userResponse = userResponse({ models: [], total: 41, page: 9, page_size: 20 });

    render(<ModelMarketplacePage />);

    expect(screen.getByText("emptyTitle")).toBeInTheDocument();
    expect(state.paginationProps).toMatchObject({ page: 9, pageSize: 20, pageCount: 3 });
  });

  it("keeps the shared 1/1 pagination visible for a successful zero-total response", () => {
    state.userResponse = userResponse({ models: [], total: 0, page: 1, page_size: 20 });

    render(<ModelMarketplacePage />);

    expect(screen.getByText("emptyTitle")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "shared-pagination" })).toHaveTextContent("1/1");
    expect(state.paginationProps).toMatchObject({ page: 1, pageSize: 20, pageCount: 1 });
  });
});
