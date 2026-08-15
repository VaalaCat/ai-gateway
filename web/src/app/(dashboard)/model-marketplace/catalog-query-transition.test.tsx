import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AdminModelMarketplaceListResponse,
  MarketplaceModel,
  ModelMarketplaceListResponse,
} from "@/lib/api/model-marketplace";
import { ApiError } from "@/lib/api/client";
import { useTokens } from "@/lib/api/tokens";
import type { Token } from "@/lib/types";
import { createTestQueryClient } from "@/test/render";
import ModelMarketplacePage from "./page";

interface PaginationProps {
  page: number;
  pageSize: number;
  pageCount: number;
  onPaginationChange: (page: number, pageSize: number) => void;
}

const state = vi.hoisted(() => ({
  query: "token_id=1",
  isAdmin: false,
  replace: vi.fn(),
  paginationProps: undefined as PaginationProps | undefined,
  translate: vi.fn((key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key),
}));
const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useSearchParams: () => new URLSearchParams(state.query),
  useRouter: () => ({ replace: state.replace }),
  notFound: vi.fn(),
}));
vi.mock("next-intl", () => ({
  useTranslations: () => state.translate,
}));
vi.mock("@/components/business/provider-avatar", () => ({
  ProviderAvatar: ({ provider, size }: { provider: string; size: number }) => (
    <svg aria-label={`${provider}-${size}`} />
  ),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({
    placeholder,
    id,
    value,
    onChange,
  }: {
    placeholder?: string;
    id?: string;
    value: string;
    onChange: (value: string) => void;
  }) => (
    <button id={id} type="button" role="combobox" onClick={() => onChange("2")}>
      {value || placeholder}
    </button>
  ),
}));
vi.mock("@/components/model-marketplace/model-card", () => ({
  ModelCard: ({ model: item }: { model: MarketplaceModel }) => {
    const name = item.kind === "real" ? item.real.metadata.display_name : item.routing.display_name;
    return <article aria-label={name}>{name}</article>;
  },
}));
vi.mock("@/components/data-table/pagination", () => ({
  DataTablePagination: (props: PaginationProps) => {
    state.paginationProps = props;
    return (
      <nav aria-label="shared-pagination">
        <output>{props.page}/{props.pageCount}:{props.pageSize}</output>
        <button
          type="button"
          onClick={() => props.onPaginationChange(props.page + 1, props.pageSize)}
        >
          test-pagination-next
        </button>
      </nav>
    );
  },
}));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => false }));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    loading: false,
    isAdmin: state.isAdmin,
    user: { user_id: 7, role: state.isAdmin ? 2 : 1 },
  }),
}));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({
    data: { model_marketplace: true },
    isLoading: false,
    isError: false,
  }),
  isModelMarketplaceVisible: () => true,
}));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { get: apiGet } };
});

function token(id = 1): Token {
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
  };
}

function model(modelName: string, displayName: string): MarketplaceModel {
  return {
    kind: "real",
    real: {
      model_name: modelName,
      metadata: {
        display_name: displayName,
        description: "",
        provider: "Provider",
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

function response(
  item: MarketplaceModel,
  page = 1,
): ModelMarketplaceListResponse {
  return {
    selected_token: { id: 1, name: "Primary" },
    models: [item],
    filters: {
      providers: ["Provider"],
      input_modalities: ["text"],
      output_modalities: ["text"],
    },
    total: 2,
    page,
    page_size: 20,
  };
}

function adminResponse(
  item: MarketplaceModel,
  tokenId?: number,
): AdminModelMarketplaceListResponse {
  return {
    view: tokenId
      ? { mode: "token_preview", selected_token: { id: tokenId, name: `Token ${tokenId}` } }
      : { mode: "global", selected_token: null },
    models: [item],
    filters: {
      providers: ["Provider"],
      input_modalities: ["text"],
      output_modalities: ["text"],
    },
    total: 1,
    page: 1,
    page_size: 20,
  };
}

function commitLastNavigation(view: ReturnType<typeof render>) {
  const target = state.replace.mock.calls.at(-1)?.[0];
  if (typeof target !== "string") throw new Error("missing router.replace target");
  state.query = target.split("?")[1] ?? "";
  view.rerender(<ModelMarketplacePage />);
}

describe("catalog query-key transition", () => {
  beforeEach(() => {
    state.query = "token_id=1";
    state.isAdmin = false;
    state.replace.mockReset();
    state.paginationProps = undefined;
    apiGet.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("debounces search and keeps previous cards busy through search and page transitions", async () => {
    let resolveSearch!: (value: ModelMarketplaceListResponse) => void;
    let resolvePageTwo!: (value: ModelMarketplaceListResponse) => void;
    const searchResponse = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolveSearch = resolve;
    });
    const pageTwoResponse = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolvePageTwo = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token()], total: 1, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return Promise.resolve(token());
      if (path === "/model-marketplace?token_id=1&page=1&page_size=20") {
        return Promise.resolve(response(model("gpt-4o", "GPT-4o")));
      }
      if (path === "/model-marketplace?token_id=1&search=claude&page=1&page_size=20") {
        return searchResponse;
      }
      if (path === "/model-marketplace?token_id=1&search=claude&page=2&page_size=20") {
        return pageTwoResponse;
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByRole("article", { name: "GPT-4o" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toBeInTheDocument();
    const input = screen.getByRole("textbox", { name: "searchLabel" });
    expect(screen.getByRole("combobox", { name: "providerLabel" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "kindLabel" })).toBeInTheDocument();
    input.focus();
    vi.useFakeTimers();
    fireEvent.change(input, { target: { value: "claude" } });

    await act(() => vi.advanceTimersByTimeAsync(299));
    expect(apiGet).not.toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=claude&page=1&page_size=20",
    );
    await act(() => vi.advanceTimersByTimeAsync(1));
    commitLastNavigation(view);
    await act(() => vi.runOnlyPendingTimersAsync());
    vi.useRealTimers();

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=claude&page=1&page_size=20",
    ));
    expect(apiGet).toHaveBeenCalledTimes(4);
    expect(screen.getByRole("article", { name: "GPT-4o" })).toBeInTheDocument();
    expect(screen.getByTestId("model-catalog-results")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("textbox", { name: "searchLabel" })).toBe(input);
    expect(document.activeElement).toBe(input);

    await act(async () => {
      resolveSearch(response(model("claude-3-7", "Claude 3.7")));
      await searchResponse;
    });
    expect(await screen.findByRole("article", { name: "Claude 3.7" })).toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "GPT-4o" })).not.toBeInTheDocument();

    act(() => state.paginationProps?.onPaginationChange(2, 20));
    commitLastNavigation(view);
    await act(() => Promise.resolve());
    expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=claude&page=2&page_size=20",
    );
    expect(screen.getByRole("article", { name: "Claude 3.7" })).toBeInTheDocument();
    expect(screen.getByTestId("model-catalog-results")).toHaveAttribute("aria-busy", "true");

    await act(async () => {
      resolvePageTwo(response(model("claude-opus", "Claude Opus"), 2));
      await pageTwoResponse;
    });
    expect(await screen.findByRole("article", { name: "Claude Opus" })).toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "Claude 3.7" })).not.toBeInTheDocument();
  });

  it("does not retain Token A cards while Token B catalog is pending", async () => {
    let resolveTokenB!: (value: ModelMarketplaceListResponse) => void;
    const tokenBResponse = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolveTokenB = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token(1), token(2)], total: 2, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return Promise.resolve(token(1));
      if (path === "/tokens/2") return Promise.resolve(token(2));
      if (path === "/model-marketplace?token_id=1&page=1&page_size=20") {
        return Promise.resolve(response(model("scope-a", "Scope A")));
      }
      if (path === "/model-marketplace?token_id=2&page=1&page_size=20") {
        return tokenBResponse;
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    queryClient.setQueryDefaults(["tokens"], { staleTime: Number.POSITIVE_INFINITY });
    queryClient.setQueryData(["tokens", 1], token(1));
    queryClient.setQueryData(["tokens", 2], token(2));
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });
    expect(await screen.findByRole("article", { name: "Scope A" })).toBeInTheDocument();

    state.query = "token_id=2";
    view.rerender(<ModelMarketplacePage />);
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=2&page=1&page_size=20",
    ));

    expect(screen.queryByRole("article", { name: "Scope A" })).not.toBeInTheDocument();
    expect(screen.getByTestId("model-catalog-results")).toHaveAttribute("aria-busy", "true");

    await act(async () => {
      resolveTokenB(response(model("scope-b", "Scope B")));
      await tokenBResponse;
    });
    expect(await screen.findByRole("article", { name: "Scope B" })).toBeInTheDocument();
  });

  it("does not retain the admin global catalog while Token preview is pending", async () => {
    state.isAdmin = true;
    state.query = "";
    let resolvePreview!: (value: AdminModelMarketplaceListResponse) => void;
    const previewResponse = new Promise<AdminModelMarketplaceListResponse>((resolve) => {
      resolvePreview = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token(2)], total: 1, page: 1, page_size: 2 });
      }
      if (path === "/tokens/2") return Promise.resolve(token(2));
      if (path === "/admin/model-marketplace?page=1&page_size=20") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      if (path === "/admin/model-marketplace?token_id=2&page=1&page_size=20") {
        return previewResponse;
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    queryClient.setQueryDefaults(["tokens"], { staleTime: Number.POSITIVE_INFINITY });
    queryClient.setQueryData(["tokens", 2], token(2));
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });
    expect(await screen.findByRole("article", { name: "Global Model" })).toBeInTheDocument();

    state.query = "token_id=2";
    view.rerender(<ModelMarketplacePage />);
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/admin/model-marketplace?token_id=2&page=1&page_size=20",
    ));

    expect(screen.queryByRole("article", { name: "Global Model" })).not.toBeInTheDocument();
    expect(screen.getByTestId("model-catalog-results")).toHaveAttribute("aria-busy", "true");

    await act(async () => {
      resolvePreview(adminResponse(model("preview-model", "Preview Model"), 2));
      await previewResponse;
    });
    expect(await screen.findByRole("article", { name: "Preview Model" })).toBeInTheDocument();
  });

  it("keeps a successful admin global catalog independent from ordinary Token bootstrap", async () => {
    state.isAdmin = true;
    state.query = "";
    apiGet.mockImplementation((path: string) => {
      if (path.startsWith("/tokens?")) {
        return Promise.reject(new ApiError(500, "bootstrap failed"));
      }
      if (path === "/admin/model-marketplace?page=1&page_size=20") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByRole("article", { name: "Global Model" })).toBeInTheDocument();
    expect(apiGet.mock.calls.map(([path]) => path)).not.toContain(
      "/tokens?page=1&page_size=2&user_id=7&usable_only=true",
    );
    expect(screen.queryByText("tokenLoadErrorTitle")).not.toBeInTheDocument();
  });

  it("ignores a cached ordinary bootstrap failure in admin global view", async () => {
    state.isAdmin = true;
    state.query = "";
    const bootstrapPath = "/tokens?page=1&page_size=2&user_id=7&usable_only=true";
    apiGet.mockImplementation((path: string) => {
      if (path === bootstrapPath) return Promise.reject(new ApiError(500, "cached failure"));
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const bootstrapView = renderHook(() => useTokens({
      page: 1,
      page_size: 2,
      user_id: 7,
      usable_only: true,
    }), { wrapper });
    await waitFor(() => expect(bootstrapView.result.current.isError).toBe(true));
    bootstrapView.unmount();

    apiGet.mockReset();
    apiGet.mockImplementation((path: string) => {
      if (path === "/admin/model-marketplace?page=1&page_size=20") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      throw new Error(`unexpected request: ${path}`);
    });
    render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByRole("article", { name: "Global Model" })).toBeInTheDocument();
    expect(screen.queryByText("tokenLoadErrorTitle")).not.toBeInTheDocument();
    expect(apiGet).not.toHaveBeenCalledWith(bootstrapPath);
  });

  it("keeps a non-404 validation failure in candidate scope and exposes retry", async () => {
    state.isAdmin = true;
    state.query = "token_id=2";
    let validationCalls = 0;
    apiGet.mockImplementation((path: string) => {
      if (path.startsWith("/tokens?")) {
        return Promise.resolve({ data: [], total: 0, page: 1, page_size: 2 });
      }
      if (path === "/tokens/2") {
        validationCalls += 1;
        return Promise.reject(new ApiError(500, "validation failed"));
      }
      if (path === "/admin/model-marketplace?page=1&page_size=20") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByText("tokenValidationErrorTitle")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("2");
    expect(screen.queryByRole("article", { name: "Global Model" })).not.toBeInTheDocument();
    expect(apiGet).not.toHaveBeenCalledWith("/admin/model-marketplace?page=1&page_size=20");
    expect(apiGet.mock.calls.map(([path]) => path)).not.toContain(
      "/tokens?page=1&page_size=2&user_id=7&usable_only=true",
    );

    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await waitFor(() => expect(validationCalls).toBe(2));
  });

  it("keeps a validated cached Token and catalog during background validation", async () => {
    state.query = "token_id=1";
    let rejectValidation!: (error: unknown) => void;
    const validation = new Promise<Token>((_resolve, reject) => {
      rejectValidation = reject;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token(1), token(2)], total: 2, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return validation;
      if (path === "/model-marketplace?token_id=1&page=1&page_size=20") {
        return Promise.resolve(response(model("cached-scope", "Cached Scope")));
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["tokens", 1], token(1));
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    render(<ModelMarketplacePage />, { wrapper });

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith("/tokens/1"));
    expect(await screen.findByRole("article", { name: "Cached Scope" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("1");

    await act(async () => {
      rejectValidation(new ApiError(500, "background failed"));
      await validation.catch(() => undefined);
    });
    expect(screen.getByRole("article", { name: "Cached Scope" })).toBeInTheDocument();
    expect(screen.queryByText("tokenValidationErrorTitle")).not.toBeInTheDocument();
  });

  it("hides the admin global catalog while an uncached Token is being validated", async () => {
    state.isAdmin = true;
    state.query = "";
    let resolveTokenValidation!: (value: Token) => void;
    let resolvePreview!: (value: AdminModelMarketplaceListResponse) => void;
    const tokenValidation = new Promise<Token>((resolve) => {
      resolveTokenValidation = resolve;
    });
    const previewResponse = new Promise<AdminModelMarketplaceListResponse>((resolve) => {
      resolvePreview = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token(2)], total: 1, page: 1, page_size: 2 });
      }
      if (path === "/tokens/2") return tokenValidation;
      if (path === "/admin/model-marketplace?page=1&page_size=20") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      if (path === "/admin/model-marketplace?token_id=2&page=1&page_size=20") {
        return previewResponse;
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    render(<ModelMarketplacePage />, { wrapper });
    expect(await screen.findByRole("article", { name: "Global Model" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith("/tokens/2"));

    expect(screen.queryByRole("article", { name: "Global Model" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("catalogLoading")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toHaveTextContent("2");
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).not.toHaveTextContent(
      "adminGlobalOption",
    );
    expect(apiGet).not.toHaveBeenCalledWith(
      "/admin/model-marketplace?token_id=2&page=1&page_size=20",
    );

    await act(async () => {
      resolveTokenValidation(token(2));
      await tokenValidation;
    });
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/admin/model-marketplace?token_id=2&page=1&page_size=20",
    ));

    await act(async () => {
      resolvePreview(adminResponse(model("preview-model", "Preview Model"), 2));
      await previewResponse;
    });
    expect(await screen.findByRole("article", { name: "Preview Model" })).toBeInTheDocument();
  });

  it.each([
    ["user expired", false, "marketplace_token_expired", "/model-marketplace?token_id=1&search=gpt&page=3&page_size=50"],
    ["user disabled", false, "marketplace_token_disabled", "/model-marketplace?token_id=1&search=gpt&page=3&page_size=50"],
    ["admin expired", true, "marketplace_token_expired", "/admin/model-marketplace?token_id=1&search=gpt&page=3&page_size=50"],
    ["admin disabled", true, "marketplace_token_disabled", "/admin/model-marketplace?token_id=1&search=gpt&page=3&page_size=50"],
  ] as const)("handles a real marketplace 422 once for the %s catalog", async (
    _role,
    isAdmin,
    code,
    rejectedPath,
  ) => {
    state.isAdmin = isAdmin;
    state.query = "token_id=1&search=gpt&page=3&page_size=50";
    window.localStorage.setItem("aigw:model-marketplace:last-token-id:7", "1");
    let bootstrapCalls = 0;
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        bootstrapCalls += 1;
        return Promise.resolve({ data: [token(1), token(2)], total: 2, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return Promise.resolve(token(1));
      if (path === rejectedPath) {
        return Promise.reject(new ApiError(422, "expired", {
          code,
        }));
      }
      if (isAdmin && path === "/admin/model-marketplace?search=gpt&page=3&page_size=50") {
        return Promise.resolve(adminResponse(model("global-model", "Global Model")));
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByText("tokenUnavailableTitle")).toBeInTheDocument();
    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(state.replace).toHaveBeenCalledWith(
      "/model-marketplace?search=gpt&page_size=50",
    );
    expect(window.localStorage.getItem("aigw:model-marketplace:last-token-id:7")).toBeNull();
    const expectedBootstrapCalls = isAdmin ? 0 : 2;
    await waitFor(() => expect(bootstrapCalls).toBe(expectedBootstrapCalls));

    view.rerender(<ModelMarketplacePage />);
    await act(() => Promise.resolve());
    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(bootstrapCalls).toBe(expectedBootstrapCalls);
  });

  it("shows target page metadata and advances from that target while page 2 is pending", async () => {
    let resolvePageTwo!: (value: ModelMarketplaceListResponse) => void;
    const pageTwo = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolvePageTwo = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token()], total: 1, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return Promise.resolve(token());
      if (path === "/model-marketplace?token_id=1&page=1&page_size=20") {
        return Promise.resolve({
          ...response(model("page-one", "Page One")),
          total: 60,
        });
      }
      if (path === "/model-marketplace?token_id=1&page=2&page_size=20") return pageTwo;
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });
    expect(await screen.findByRole("article", { name: "Page One" })).toBeInTheDocument();

    act(() => state.paginationProps?.onPaginationChange(2, 20));
    commitLastNavigation(view);
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&page=2&page_size=20",
    ));

    expect(screen.getByRole("navigation", { name: "shared-pagination" }))
      .toHaveTextContent("2/3:20");
    fireEvent.click(screen.getByRole("button", { name: "test-pagination-next" }));
    expect(state.replace).toHaveBeenLastCalledWith("/model-marketplace?token_id=1&page=3");

    await act(async () => {
      resolvePageTwo({
        ...response(model("page-two", "Page Two"), 2),
        total: 60,
      });
      await pageTwo;
    });
    expect(await screen.findByRole("article", { name: "Page Two" })).toBeInTheDocument();
  });

  it("shows target page size and uses it for next while 20 to 50 is pending", async () => {
    let resolveFifty!: (value: ModelMarketplaceListResponse) => void;
    const fifty = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolveFifty = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=2&user_id=7&usable_only=true") {
        return Promise.resolve({ data: [token()], total: 1, page: 1, page_size: 2 });
      }
      if (path === "/tokens/1") return Promise.resolve(token());
      if (path === "/model-marketplace?token_id=1&page=1&page_size=20") {
        return Promise.resolve({
          ...response(model("twenty", "Twenty")),
          total: 120,
        });
      }
      if (path === "/model-marketplace?token_id=1&page=1&page_size=50") return fifty;
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });
    expect(await screen.findByRole("article", { name: "Twenty" })).toBeInTheDocument();

    act(() => state.paginationProps?.onPaginationChange(1, 50));
    commitLastNavigation(view);
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&page=1&page_size=50",
    ));

    expect(screen.getByRole("navigation", { name: "shared-pagination" }))
      .toHaveTextContent("1/3:50");
    fireEvent.click(screen.getByRole("button", { name: "test-pagination-next" }));
    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=1&page_size=50&page=2",
    );

    await act(async () => {
      resolveFifty({
        ...response(model("fifty", "Fifty")),
        total: 120,
        page_size: 50,
      });
      await fifty;
    });
    expect(await screen.findByRole("article", { name: "Fifty" })).toBeInTheDocument();
  });
});
