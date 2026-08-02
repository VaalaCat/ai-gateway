import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  MarketplaceModel,
  ModelMarketplaceListResponse,
} from "@/lib/api/model-marketplace";
import type { Token } from "@/lib/types";
import { createTestQueryClient } from "@/test/render";
import ModelMarketplacePage from "./page";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useSearchParams: () => new URLSearchParams("token_id=1"),
  useRouter: () => ({ replace: vi.fn() }),
  notFound: vi.fn(),
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
    isAdmin: false,
    user: { user_id: 7, role: 1 },
  }),
}));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({
    data: {
      token: { can_edit_model_whitelist: false },
      model_marketplace: true,
    },
    isLoading: false,
    isError: false,
  }),
  isModelMarketplaceVisible: () => true,
}));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { get: apiGet } };
});

function token(): Token {
  return {
    id: 1,
    user_id: 7,
    key: "key-1",
    name: "Primary",
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

function response(item: MarketplaceModel): ModelMarketplaceListResponse {
  return {
    selected_token: { id: 1, name: "Primary" },
    models: [item],
    filters: {
      providers: ["Provider"],
      input_modalities: ["text"],
      output_modalities: ["text"],
    },
  };
}

describe("catalog query-key transition", () => {
  beforeEach(() => apiGet.mockReset());

  it("keeps the real search input focused while a deferred keyed response replaces old cards", async () => {
    let resolveSearch!: (value: ModelMarketplaceListResponse) => void;
    const searchResponse = new Promise<ModelMarketplaceListResponse>((resolve) => {
      resolveSearch = resolve;
    });
    apiGet.mockImplementation((path: string) => {
      if (path === "/tokens?page=1&page_size=100") {
        return Promise.resolve({ data: [token()], total: 1, page: 1, page_size: 100 });
      }
      if (path === "/model-marketplace?token_id=1") {
        return Promise.resolve(response(model("gpt-4o", "GPT-4o")));
      }
      if (path === "/model-marketplace?token_id=1&search=claude") {
        return searchResponse;
      }
      if (path === undefined) return Promise.resolve(undefined);
      throw new Error(`unexpected request: ${path}`);
    });
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = render(<ModelMarketplacePage />, { wrapper });

    expect(await screen.findByRole("article", { name: "GPT-4o" })).toBeInTheDocument();
    const input = screen.getByRole("searchbox", { name: "searchLabel" });
    input.focus();
    fireEvent.change(input, { target: { value: "claude" } });

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      "/model-marketplace?token_id=1&search=claude",
    ));
    expect(screen.getByRole("searchbox", { name: "searchLabel" })).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(screen.getByLabelText("catalogLoading")).toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "GPT-4o" })).not.toBeInTheDocument();

    await act(async () => {
      resolveSearch(response(model("claude-3-7", "Claude 3.7")));
      await searchResponse;
    });

    expect(await screen.findByRole("article", { name: "Claude 3.7" })).toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "GPT-4o" })).not.toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "searchLabel" })).toBe(input);
    expect(document.activeElement).toBe(input);
    view.unmount();
    expect(apiGet.mock.calls
      .map(([path]) => path)
      .filter((path): path is string => typeof path === "string"))
      .toEqual([
        "/tokens?page=1&page_size=100",
        "/model-marketplace?token_id=1",
        "/model-marketplace?token_id=1&search=claude",
      ]);
  });
});
