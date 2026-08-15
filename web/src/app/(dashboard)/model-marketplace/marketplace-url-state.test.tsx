import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { useMarketplaceTokenSelection } from "@/components/model-marketplace/use-marketplace-token-selection";
import type { Token } from "@/lib/types";

const state = vi.hoisted(() => ({
  query: "token_id=1&page=3",
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => "/model-marketplace",
  useRouter: () => ({ replace: state.replace }),
  useSearchParams: () => new URLSearchParams(state.query),
}));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: false }) }));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: () => ({
    data: { data: [token(1), token(9)], total: 2, page: 1, page_size: 2 },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useToken: (id: number) => ({
    data: id > 0 ? token(id) : undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: undefined,
  }),
}));
vi.mock("@/components/model-marketplace/use-token-expiry-clock", () => ({
  useTokenExpiryClock: () => 1_000,
}));

function token(id: number): Token {
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

function Harness() {
  const patchSearchParams = useSearchParamPatch();
  const [filters, setFilters] = useFilterState(
    {
      search: { kind: "text", debounceMs: 300 },
      provider: { kind: "text" },
      kind: { kind: "text" },
    },
    { patchSearchParams },
  );
  const [, , setPagination] = usePaginationState(20, { patchSearchParams });
  const tokenSelection = useMarketplaceTokenSelection(7, false, patchSearchParams);

  return (
    <>
      <FilterableToolbar
        spec={{ search: { kind: "text", label: "Search", debounceMs: 300 } }}
        value={filters}
        onChange={setFilters}
      />
      <button
        type="button"
        onClick={() => {
          setFilters({ provider: "OpenAI" });
          tokenSelection.handleTokenChange(9);
        }}
      >
        filter then Token
      </button>
      <button
        type="button"
        onClick={() => {
          tokenSelection.handleTokenChange(9);
          setPagination(1, 50);
        }}
      >
        Token then page size
      </button>
      <button type="button" onClick={() => setPagination(1, 50)}>
        page size
      </button>
      <button
        type="button"
        onClick={() => {
          setFilters({ provider: "OpenAI" });
          setFilters({ kind: "real" });
        }}
      >
        provider then kind
      </button>
      <button type="button" onClick={() => setFilters({ provider: "OpenAI" })}>
        provider
      </button>
      <button
        type="button"
        onClick={() => {
          setFilters({ provider: "OpenAI" });
          setFilters({ kind: "" });
        }}
      >
        provider then clear kind
      </button>
      <output aria-label="selected Token">{tokenSelection.selectedTokenId ?? "none"}</output>
    </>
  );
}

describe("marketplace shared URL updates", () => {
  beforeEach(() => {
    state.query = "token_id=1&page=3";
    state.replace.mockReset();
    window.localStorage.clear();
    vi.useFakeTimers();
  });
  afterEach(() => vi.useRealTimers());

  it("keeps both a filter and Token update issued before navigation commits", () => {
    const view = render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "filter then Token" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=9&provider=OpenAI",
    );

    state.query = "token_id=1&provider=OpenAI";
    view.rerender(<Harness />);
    expect(screen.getByRole("status", { name: "selected Token" })).toHaveTextContent("9");
  });

  it("keeps both a Token and page-size update issued before navigation commits", () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "Token then page size" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=9&page_size=50",
    );
  });

  it("merges a debounced search with pagination changed before the debounce fires", () => {
    render(<Harness />);

    fireEvent.change(screen.getByRole("textbox", { name: "Search" }), {
      target: { value: "claude" },
    });
    fireEvent.click(screen.getByRole("button", { name: "page size" }));
    act(() => vi.advanceTimersByTime(300));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=1&page_size=50&search=claude",
    );
  });

  it("keeps provider and kind updates issued by the same filter writer", () => {
    state.query = "token_id=1&page=3&provider=Legacy";
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "provider then kind" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=1&provider=OpenAI&kind=real",
    );
  });

  it("keeps a pending provider when the debounced search update is issued", () => {
    state.query = "token_id=1&page=3&provider=Legacy";
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "provider" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Search" }), {
      target: { value: "claude" },
    });
    act(() => vi.advanceTimersByTime(300));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=1&provider=OpenAI&search=claude",
    );
  });

  it("clears only the requested filter without overwriting another pending filter", () => {
    state.query = "token_id=1&page=3&provider=Legacy&kind=routing";
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "provider then clear kind" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/model-marketplace?token_id=1&provider=OpenAI",
    );
  });
});
