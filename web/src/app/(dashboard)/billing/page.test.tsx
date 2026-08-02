import type { ReactNode } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import BillingPage from "./page";

const state = vi.hoisted(() => ({
  query: "token_id=11&trend_token_id=22&model=gpt-5",
  replace: vi.fn(),
  insights: vi.fn(),
  overview: vi.fn(),
  tokenBilling: vi.fn(),
  channelBilling: vi.fn(),
  rebuildJobs: vi.fn(),
  rebuildRunning: false,
  topN: 10,
  setTopN: vi.fn(),
  isAdmin: true,
  range: { start: 1_700_000_000, end: 1_700_604_800, gran: "day" as const },
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
  useChartTopN: () => [state.topN, state.setTopN],
}));
vi.mock("@/lib/hooks/use-obs-range", () => ({
  useObsRange: () => ({
    range: state.range,
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
  useRebuildBillingJobs: () => {
    state.rebuildJobs();
    return {
      data: {
        jobs: state.rebuildRunning ? [{ status: "running" }] : [],
      },
    };
  },
}));
vi.mock("@/lib/api/channels", () => ({
  useChannelTypes: () => ({ data: [] }),
}));
vi.mock("@/components/business/observability-header", () => ({
  ObservabilityHeader: ({ scopeLabel, scopeControls, headerActions }: {
    scopeLabel?: string;
    scopeControls?: ReactNode;
    headerActions?: ReactNode;
  }) => (
    <div>
      {scopeControls && (
        <div data-slot="page-scope-rail">
          <span>{scopeLabel}</span>
          {scopeControls}
        </div>
      )}
      <div data-slot="header-actions">{headerActions}</div>
    </div>
  ),
}));
vi.mock("@/components/business/kpi-grid", () => ({ KpiGrid: () => null }));
vi.mock("@/components/business/rebuild-dialog", () => ({
  RebuildDialog: ({ open }: { open: boolean }) => open ? (
    <div role="dialog" aria-label="rebuildConfirm">rebuildConfirm</div>
  ) : null,
}));
vi.mock("@/components/data-table/data-table", () => ({ DataTable: () => null }));
vi.mock("@/components/business/metric-trend-chart", () => ({
  MetricTrendChart: ({ scopeControls, displayExtra }: {
    scopeControls?: ReactNode;
    displayExtra?: ReactNode;
  }) => (
    <div>
      <div data-slot="chart-scope-controls">{scopeControls}</div>
      <div data-slot="chart-display-controls">{displayExtra}</div>
    </div>
  ),
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
  ChartOptionSelect: ({ label, value, onValueChange }: {
    label: string;
    value: string;
    onValueChange?: (value: string) => void;
  }) => (
    <button type="button" aria-label={label} onClick={() => onValueChange?.("20")}>{value}</button>
  ),
}));

beforeEach(() => {
  state.query = "token_id=11&trend_token_id=22&model=gpt-5";
  state.replace.mockReset();
  state.insights.mockReset();
  state.overview.mockReset();
  state.tokenBilling.mockReset();
  state.channelBilling.mockReset();
  state.rebuildJobs.mockReset();
  state.rebuildRunning = false;
  state.topN = 10;
  state.setTopN.mockReset();
  state.isAdmin = true;
  state.range = { start: 1_700_000_000, end: 1_700_604_800, gran: "day" };
});

it("preserves an explicit single-day range in billing insights", () => {
  state.query = "start=1700000000&end=1700086399&gran=day";
  state.range = { start: 1_700_000_000, end: 1_700_086_399, gran: "day" };

  render(<BillingPage />);

  expect(state.insights.mock.calls.at(-1)?.[0]).toEqual(
    expect.objectContaining({ from: 1_700_000_000, to: 1_700_086_399 }),
  );
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

it("keeps only the admin user filter in the page scope rail", () => {
  state.query = "user_id=42&token_id=11&trend_token_id=22&model=gpt-5";
  const { container } = render(<BillingPage />);

  const rail = container.querySelector('[data-slot="page-scope-rail"]');
  expect(rail).toHaveTextContent("filters");
  expect(rail?.querySelector('[data-entity="user"]')).toHaveAttribute("data-value", "42");
  expect(rail).not.toHaveTextContent("prefix.topN");
});

it("does not render a page scope rail for a non-admin billing user", () => {
  state.isAdmin = false;
  const { container } = render(<BillingPage />);

  expect(container.querySelector('[data-slot="page-scope-rail"]')).not.toBeInTheDocument();
});

it("opens the existing rebuild confirmation from the header actions menu", async () => {
  const user = userEvent.setup();
  const { container } = render(<BillingPage />);

  const actions = container.querySelector('[data-slot="header-actions"]');
  const trigger = within(actions as HTMLElement).getByRole("button", { name: "more" });
  await user.click(trigger);
  fireEvent.click(await screen.findByRole("menuitem", { name: "rebuild" }));

  expect(screen.getByRole("dialog", { name: "rebuildConfirm" })).toBeInTheDocument();
});

it("keeps rebuild job polling mounted while the header menu is closed", () => {
  state.rebuildRunning = true;
  render(<BillingPage />);

  expect(state.rebuildJobs).toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "more" })).toHaveAttribute("data-running", "true");
});

it("renders rebuild as a keyboard-navigable dropdown menu item", async () => {
  const user = userEvent.setup();
  render(<BillingPage />);

  await user.click(screen.getByRole("button", { name: "more" }));

  expect(await screen.findByRole("menuitem")).toHaveTextContent("rebuild");
});

it("renders model, API token, and top N only in the trend scope controls", () => {
  const { container } = render(<BillingPage />);

  const scope = container.querySelector('[data-slot="chart-scope-controls"]');
  expect(scope?.querySelector('[data-entity="model"]')).toBeInTheDocument();
  expect(scope?.querySelector('[data-entity="token"]')).toBeInTheDocument();
  expect(within(scope as HTMLElement).getByRole("button", { name: "prefix.topN" })).toBeInTheDocument();
  expect(container.querySelector('[data-slot="page-scope-rail"]')).not.toHaveTextContent("prefix.topN");
});

it("changes trend top N without adding trend-only parameters to billing tables", () => {
  render(<BillingPage />);

  fireEvent.click(screen.getByRole("button", { name: "prefix.topN" }));

  expect(state.setTopN).toHaveBeenCalledWith(20);
  expect(state.overview.mock.calls.at(-1)?.[0]).not.toHaveProperty("top_n");
  expect(state.tokenBilling.mock.calls.at(-1)?.[0]).not.toHaveProperty("top_n");
  expect(state.channelBilling.mock.calls.at(-1)?.[0]).not.toHaveProperty("top_n");
});
