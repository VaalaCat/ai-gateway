import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import BillingPage from "./page";

const state = vi.hoisted(() => ({
  query: "token_id=11&trend_token_id=22&model=gpt-5",
  replace: vi.fn(),
  insights: vi.fn(),
  overview: vi.fn(),
  tokenBilling: vi.fn(),
  channelBilling: vi.fn(),
  isAdmin: true,
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: state.replace }),
  useSearchParams: () => new URLSearchParams(state.query),
  usePathname: () => "/billing",
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ user: { user_id: 7 }, isAdmin: state.isAdmin, loading: false }),
}));
vi.mock("@/lib/hooks/use-chart-top-n", () => ({
  useChartTopN: () => [10, vi.fn()],
}));
vi.mock("@/lib/hooks/use-obs-range", () => ({
  useObsRange: () => ({
    range: { start: 1_700_000_000, end: 1_700_604_800, gran: "day" },
    setRange: vi.fn(),
    refresh: vi.fn(),
    refreshKey: 0,
  }),
}));
vi.mock("@/lib/api/billing-insights", () => ({
  useBillingInsights: (params: unknown) => {
    state.insights(params);
    return { data: undefined, isLoading: false, isFetching: false };
  },
}));
vi.mock("@/lib/api/billing", () => ({
  useBillingOverview: (params: unknown) => {
    state.overview(params);
    return { data: undefined, isError: false, isFetching: false };
  },
  useTokenBilling: (params: unknown) => {
    state.tokenBilling(params);
    return { data: { data: [], total: 0 }, isLoading: false, isError: false };
  },
  useChannelBilling: (params: unknown) => {
    state.channelBilling(params);
    return { data: { data: [], total: 0 }, isLoading: false, isError: false };
  },
}));
vi.mock("@/lib/api/channels", () => ({
  useChannelTypes: () => ({ data: [] }),
}));
vi.mock("@/components/business/observability-header", () => ({
  ObservabilityHeader: ({ extraFilters }: { extraFilters?: ReactNode }) => <div>{extraFilters}</div>,
}));
vi.mock("@/components/business/kpi-grid", () => ({ KpiGrid: () => null }));
vi.mock("@/components/business/rebuild-button", () => ({ RebuildButton: () => null }));
vi.mock("@/components/business/rebuild-dialog", () => ({ RebuildDialog: () => null }));
vi.mock("@/components/data-table/data-table", () => ({ DataTable: () => null }));
vi.mock("@/components/business/metric-trend-chart", () => ({
  MetricTrendChart: ({ headerExtra }: { headerExtra?: ReactNode }) => <div>{headerExtra}</div>,
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ entity, size, value, onChange, ownerUserId }: {
    entity: string;
    size?: string;
    value?: string;
    onChange?: (value: string) => void;
    ownerUserId?: number;
  }) => (
    <button
      type="button"
      data-entity={entity}
      data-size={size ?? "default"}
      data-value={value}
      data-owner-user-id={ownerUserId}
      onClick={() => onChange?.("")}
    >
      {entity}
    </button>
  ),
}));
vi.mock("@/components/business/chart-option-select", () => ({
  ChartOptionSelect: ({ label, value }: { label: string; value: string }) => (
    <button type="button" aria-label={label}>{value}</button>
  ),
}));

beforeEach(() => {
  state.query = "token_id=11&trend_token_id=22&model=gpt-5";
  state.replace.mockReset();
  state.insights.mockReset();
  state.overview.mockReset();
  state.tokenBilling.mockReset();
  state.channelBilling.mockReset();
  state.isAdmin = true;
});

it("renders the billing trend model filter at the compact toolbar height", () => {
  render(<BillingPage />);

  expect(screen.getByRole("button", { name: "model" })).toHaveAttribute("data-size", "sm");
});

it("applies the API token and top n only to billing trend queries", () => {
  render(<BillingPage />);

  expect(state.insights).toHaveBeenCalledWith(expect.objectContaining({
    token_id: 22,
    top_n: 10,
  }));
  expect(state.overview.mock.calls[0][0]).not.toHaveProperty("token_id");
  expect(state.tokenBilling).toHaveBeenCalledWith(expect.objectContaining({ token_id: 11 }));
  expect(state.channelBilling.mock.calls[0][0]).not.toHaveProperty("token_id");
});

it("clears only the trend token query param when the trend picker is emptied", () => {
  render(<BillingPage />);

  const trendTokenPicker = screen
    .getAllByRole("button", { name: "token" })
    .find((button) => button.getAttribute("data-value") === "22");
  expect(trendTokenPicker).toBeDefined();
  fireEvent.click(trendTokenPicker!);

  const replaced = state.replace.mock.calls.find(([url]) =>
    String(url).includes("token_id=11") && !String(url).includes("trend_token_id"),
  );
  expect(replaced).toEqual(["/billing?token_id=11&model=gpt-5", { scroll: false }]);
});

it("scopes trend tokens to the selected owner and clears the token when owner changes", () => {
  state.query = "user_id=42&token_id=11&trend_token_id=22&model=gpt-5";
  render(<BillingPage />);

  const trendTokenPicker = screen
    .getAllByRole("button", { name: "token" })
    .find((button) => button.getAttribute("data-value") === "22");
  expect(trendTokenPicker).toHaveAttribute("data-owner-user-id", "42");
  expect(state.insights.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ user_id: 42 }));
  expect(state.overview.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ user_id: 42 }));
  expect(state.tokenBilling.mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ user_id: 42 }));

  const userPicker = screen
    .getAllByRole("button", { name: "user" })
    .find((button) => button.getAttribute("data-value") === "42");
  expect(userPicker).toBeDefined();
  fireEvent.click(userPicker!);

  expect(state.replace).toHaveBeenCalledWith(
    "/billing?token_id=11&model=gpt-5",
    { scroll: false },
  );
});

it("ignores a stale user filter for ordinary billing queries and token ownership", () => {
  state.isAdmin = false;
  state.query = "user_id=42&token_id=11&trend_token_id=22&model=gpt-5";
  render(<BillingPage />);

  expect(state.insights.mock.calls.at(-1)?.[0]).not.toHaveProperty("user_id");
  expect(state.overview.mock.calls.at(-1)?.[0]).not.toHaveProperty("user_id");
  expect(state.tokenBilling.mock.calls.at(-1)?.[0]).not.toHaveProperty("user_id");
  const trendTokenPicker = screen
    .getAllByRole("button", { name: "token" })
    .find((button) => button.getAttribute("data-value") === "22");
  expect(trendTokenPicker).not.toHaveAttribute("data-owner-user-id");
});
