import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  BillingOverviewQueryParams,
  BillingTokenQueryParams,
  Token,
} from "@/lib/types";
import TokensPage from "./page";

interface BillingQueryOptions {
  enabled?: boolean;
}

interface BillingOverviewCall {
  params: BillingOverviewQueryParams;
  options: BillingQueryOptions;
}

interface TokenBillingCall {
  params: BillingTokenQueryParams;
  options: BillingQueryOptions;
}

const { state } = vi.hoisted(() => ({
  state: {
    isAdmin: true,
    token: null as Token | null,
    create: vi.fn(),
    update: vi.fn(),
    billingOverviewCalls: [] as BillingOverviewCall[],
    tokenBillingCalls: [] as TokenBillingCall[],
  },
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    user: { user_id: 7 },
    isAdmin: state.isAdmin,
    loading: false,
  }),
}));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({ data: { token: { can_edit_model_whitelist: true } } }),
}));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: () => ({
    data: { data: state.token ? [state.token] : [], total: state.token ? 1 : 0 },
    isLoading: false,
  }),
  useCreateToken: () => ({ mutateAsync: state.create, isPending: false }),
  useUpdateToken: () => ({ mutateAsync: state.update, isPending: false }),
  useDeleteToken: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/lib/api/token-templates", () => ({
  useEnabledTokenTemplates: () => ({ data: { data: [{ id: 5, byok_only: false }] } }),
}));
vi.mock("@/lib/api/billing", () => ({
  useBillingOverview: (
    params: BillingOverviewQueryParams,
    options: BillingQueryOptions,
  ) => {
    state.billingOverviewCalls.push({ params, options });
    return { data: undefined, isError: false };
  },
  useTokenBilling: (
    params: BillingTokenQueryParams,
    options: BillingQueryOptions,
  ) => {
    state.tokenBillingCalls.push({ params, options });
    return {
      data: { data: [], total: 0 },
      isLoading: false,
      isError: false,
    };
  },
}));
vi.mock("@/components/business/date-picker/date-range-picker", () => ({
  DateRangePicker: ({
    label,
    onValueChange,
  }: {
    label?: string;
    onValueChange: (value: { startDate: string; endDate: string }) => void;
  }) => (
    <button
      type="button"
      onClick={() => onValueChange({ startDate: "2026-07-20", endDate: "2026-07-25" })}
    >
      {label}
    </button>
  ),
  isDateRangeValid: ({ startDate, endDate }: { startDate: string; endDate: string }) =>
    (!startDate && !endDate) || Boolean(startDate && endDate && startDate <= endDate),
}));
vi.mock("@/components/data-table/use-filter-state", () => ({
  useFilterState: () => [{}, vi.fn()],
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: ({ primaryAction }: { primaryAction?: ReactNode }) => primaryAction,
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({
    columns,
    data,
    toolbar,
    page,
    pageSize,
    onPaginationChange,
  }: {
    columns: Array<{ id?: string; cell?: (context: unknown) => ReactNode }>;
    data: Token[];
    toolbar?: ReactNode;
    page?: number;
    pageSize?: number;
    onPaginationChange?: (page: number, pageSize: number) => void;
  }) => {
    const action = columns.find((column) => column.id === "actions");
    const isBillingTable = columns.some(
      (column) => (column as { accessorKey?: string }).accessorKey === "token_name",
    );
    return (
      <div>
        {toolbar}
        {data[0] && action?.cell?.({ row: { original: data[0] } })}
        {isBillingTable && (
          <button
            type="button"
            onClick={() => onPaginationChange?.(2, pageSize ?? 10)}
          >
            billing-page-{page}
          </button>
        )}
      </div>
    );
  },
}));
vi.mock("@/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => (
    <button type="button" onClick={onClick}>{children}</button>
  ),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ entity, onChange }: { entity: string; onChange: (value: string) => void }) => (
    <button type="button" onClick={() => onChange(entity === "token-template" ? "5" : "7")}>
      {`pick-${entity}`}
    </button>
  ),
}));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: () => null,
}));
vi.mock("@/components/business/status-select", () => ({ StatusSelect: () => null }));
vi.mock("@/components/business/date-picker/date-time-picker", () => ({
  DateTimePicker: () => null,
}));
vi.mock("@/components/ui/tag-input", () => ({ TagInput: () => null }));
vi.mock("@/components/agent-route-editor", () => ({ AgentRouteEditor: () => null }));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/token-detail-panel", () => ({ TokenDetailPanel: () => null }));

function token(traceMode: unknown): Token {
  return {
    id: 23,
    user_id: 7,
    key: "sk-test",
    name: "production",
    status: 1,
    expired_at: 0,
    models: "",
    trace_enabled: true,
    trace_mode: traceMode as Token["trace_mode"],
    created_at: 1,
    updated_at: 1,
  };
}

async function selectHeaders() {
  await userEvent.click(screen.getByRole("switch", { name: "traceEnabled" }));
  await userEvent.click(screen.getByRole("radio", { name: "traceModeHeaders" }));
}

describe("TokensPage trace mode payloads", () => {
  beforeEach(() => {
    state.isAdmin = true;
    state.token = null;
    state.create.mockReset();
    state.create.mockResolvedValue({});
    state.update.mockReset();
    state.update.mockResolvedValue({});
    state.billingOverviewCalls = [];
    state.tokenBillingCalls = [];
  });

  it.each([
    { role: "admin", isAdmin: true },
    { role: "user", isAdmin: false },
  ])("sends headers in the $role create payload and resets mode to full", async ({ isAdmin }) => {
    state.isAdmin = isAdmin;
    render(<TokensPage />);

    await userEvent.click(screen.getByRole("button", { name: "createToken" }));
    if (!isAdmin) {
      await userEvent.click(screen.getByRole("button", { name: "pick-token-template" }));
    }
    await selectHeaders();
    await userEvent.click(screen.getByRole("button", { name: "save" }));

    await waitFor(() => expect(state.create).toHaveBeenCalled());
    expect(state.create.mock.calls[0][0]).toMatchObject({
      trace_enabled: true,
      trace_mode: "headers",
    });

    await userEvent.click(screen.getByRole("button", { name: "createToken" }));
    await userEvent.click(screen.getByRole("switch", { name: "traceEnabled" }));
    expect(screen.getByRole("radio", { name: "traceModeFull" })).toHaveAttribute(
      "data-state",
      "on",
    );
  });

  it.each([
    { role: "admin", isAdmin: true },
    { role: "user", isAdmin: false },
  ])("sends headers in the $role edit payload", async ({ isAdmin }) => {
    state.isAdmin = isAdmin;
    state.token = token("headers");
    render(<TokensPage />);

    await userEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(screen.getByRole("radio", { name: "traceModeHeaders" })).toHaveAttribute(
      "data-state",
      "on",
    );
    await userEvent.click(screen.getByRole("button", { name: "save" }));

    await waitFor(() => expect(state.update).toHaveBeenCalled());
    expect(state.update.mock.calls[0][0]).toMatchObject({
      id: 23,
      trace_enabled: true,
      trace_mode: "headers",
    });
  });

  it.each([undefined, "future"])(
    "falls back to full when editing legacy trace mode %s",
    async (traceMode) => {
      state.token = token(traceMode);
      render(<TokensPage />);

      await userEvent.click(screen.getByRole("button", { name: "edit" }));

      expect(screen.getByRole("radio", { name: "traceModeFull" })).toHaveAttribute(
        "data-state",
        "on",
      );
    },
  );
});

describe("TokensPage billing date range", () => {
  beforeEach(() => {
    state.isAdmin = false;
    state.token = null;
    state.billingOverviewCalls = [];
    state.tokenBillingCalls = [];
  });

  it("updates both query boundaries atomically and resets billing pagination", async () => {
    render(<TokensPage />);

    await userEvent.click(screen.getByRole("button", { name: "billing-page-1" }));
    await waitFor(() =>
      expect(state.tokenBillingCalls.at(-1)?.params).toMatchObject({ page: 2 }),
    );

    const overviewCallCount = state.billingOverviewCalls.length;
    const tokenBillingCallCount = state.tokenBillingCalls.length;
    await userEvent.click(screen.getByRole("button", { name: "dateRange" }));

    await waitFor(() => {
      expect(state.billingOverviewCalls.length).toBeGreaterThan(overviewCallCount);
      expect(state.tokenBillingCalls.length).toBeGreaterThan(tokenBillingCallCount);
    });

    const newOverviewCalls = state.billingOverviewCalls.slice(overviewCallCount);
    for (const { params, options } of newOverviewCalls) {
      expect(Boolean(params.start_date)).toBe(Boolean(params.end_date));
      if (options.enabled) {
        expect(params).toEqual({
          start_date: "2026-07-20",
          end_date: "2026-07-25",
        });
      }
    }

    const newTokenBillingCalls = state.tokenBillingCalls.slice(tokenBillingCallCount);
    for (const { params, options } of newTokenBillingCalls) {
      expect(Boolean(params.start_date)).toBe(Boolean(params.end_date));
      if (options.enabled) {
        expect(params).toMatchObject({
          page: 1,
          start_date: "2026-07-20",
          end_date: "2026-07-25",
        });
      }
      if (params.start_date || params.end_date) {
        expect(params).toMatchObject({
          page: 1,
          start_date: "2026-07-20",
          end_date: "2026-07-25",
        });
      }
    }

    expect(state.billingOverviewCalls.at(-1)).toEqual({
      params: {
        start_date: "2026-07-20",
        end_date: "2026-07-25",
      },
      options: { enabled: true },
    });
    expect(state.tokenBillingCalls.at(-1)).toMatchObject({
      params: {
        page: 1,
        start_date: "2026-07-20",
        end_date: "2026-07-25",
      },
      options: { enabled: true },
    });
  });
});
