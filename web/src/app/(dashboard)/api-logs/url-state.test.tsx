import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { tsToDateStr } from "@/lib/utils/date-range";
import APILogsPage from "./page";

const state = vi.hoisted(() => ({
  query: "",
  replace: vi.fn(),
  logQueries: [] as Array<Record<string, unknown>>,
  dateRangeValue: undefined as { startDate: string; endDate: string } | undefined,
  onDateRangeChange: undefined as ((range: { startDate: string; endDate: string }) => void) | undefined,
}));

vi.mock("next/navigation", () => ({
  usePathname: () => "/api-logs",
  useRouter: () => ({ replace: state.replace }),
  useSearchParams: () => new URLSearchParams(state.query),
}));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 }, isAdmin: true }) }));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({
    data: { generic_api: { logs: true } },
    error: null,
    isPending: false,
    isLoading: false,
  }),
}));
vi.mock("@/lib/api/api-logs", () => ({
  useAPIRequestLogs: (query: Record<string, unknown>) => {
    state.logQueries.push(query);
    return {
      data: { data: [], total: 0, page: 1, page_size: 10 },
      error: null,
      isLoading: false,
      isFetching: false,
    };
  },
  useAPIRequestTrace: () => ({ data: undefined, error: null, isLoading: false }),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({
    id,
    entity,
    value,
    onChange,
    disabled,
  }: {
    id?: string;
    entity: string;
    value: string;
    onChange: (value: string) => void;
    disabled?: boolean;
  }) => (
    <button
      id={id}
      type="button"
      disabled={disabled}
      data-entity={entity}
      data-value={value}
      onClick={() => onChange(entity === "api-service" ? "8" : "9")}
    >
      {entity}
    </button>
  ),
}));
vi.mock("@/components/business/date-picker/date-range-picker", () => ({
  DateRangePicker: ({
    value,
    onValueChange,
  }: {
    value: { startDate: string; endDate: string };
    onValueChange: (range: { startDate: string; endDate: string }) => void;
  }) => {
    state.dateRangeValue = value;
    state.onDateRangeChange = onValueChange;
    return (
      <button
        type="button"
        onClick={() => onValueChange({ startDate: "2026-01-01", endDate: "2026-01-01" })}
      >
        dateRange
      </button>
    );
  },
}));
vi.mock("./_components/trace-dialog", () => ({ TraceDialog: () => null }));

describe("API Logs URL state", () => {
  function entityButton(entity: string) {
    const button = document.querySelector<HTMLButtonElement>(`[data-entity="${entity}"]`);
    expect(button).not.toBeNull();
    return button!;
  }

  function openAdvancedFilters() {
    const trigger = document.querySelector<HTMLButtonElement>('[data-slot="popover-trigger"]');
    expect(trigger).not.toBeNull();
    fireEvent.click(trigger!);
  }

  beforeEach(() => {
    state.query = "";
    state.replace.mockReset();
    state.logQueries = [];
    state.dateRangeValue = undefined;
    state.onDateRangeChange = undefined;
    vi.useFakeTimers();
  });

  afterEach(() => vi.useRealTimers());

  it("maps a copied URL to typed list filters, including deleted entity IDs and status zero", () => {
    state.query = "request_id=req-old&api_service_id=7&api_route_id=9&api_upstream_id=11&token_id=12&status_code=0&start=1000&end=2000&page=3&page_size=50";

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toEqual({
      request_id: "req-old",
      api_service_id: 7,
      api_route_id: 9,
      api_upstream_id: 11,
      token_id: 12,
      status_code: 0,
      start: 1_000,
      end: 2_000,
      page: 3,
      page_size: 50,
    });
  });

  it("passes an opaque request ID without trimming or rewriting Unicode boundaries", () => {
    const requestID = "\u00a0request?#/percent%\u00a0";
    state.query = new URLSearchParams({ request_id: requestID }).toString();

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toMatchObject({ request_id: requestID });
  });

  it("uses one fixed rolling seven-day range by default without writing it to the URL", () => {
    const mountedAt = new Date(2026, 6, 20, 12, 34, 56);
    vi.setSystemTime(mountedAt);
    const end = Math.floor(mountedAt.getTime() / 1_000);
    const start = end - 7 * 86_400;

    const view = render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toMatchObject({ start, end });
    expect(state.replace).not.toHaveBeenCalled();

    openAdvancedFilters();
    expect(state.dateRangeValue).toEqual({ startDate: tsToDateStr(start), endDate: tsToDateStr(end - 1) });

    vi.setSystemTime(new Date(2026, 6, 21, 12, 0, 0));
    view.rerender(<APILogsPage />);
    expect(state.logQueries.at(-1)).toMatchObject({ start, end });
  });

  it("omits empty, zero, negative, fractional, and malformed numeric filters from the API query", () => {
    state.query = "api_service_id=0&api_route_id=-1&api_upstream_id=1.5&token_id=broken&status_code=-1&start=broken&end=-20&page=0&page_size=invalid";

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toEqual({ page: 1, page_size: 10 });
  });

  it.each(["1e3", "0x10", "+7", " 7", "01", "1.5", "-1", "9007199254740992"])(
    "rejects non-strict decimal value %s before constructing the API query",
    (value) => {
      state.query = new URLSearchParams({
        api_service_id: value,
        api_route_id: value,
        api_upstream_id: value,
        token_id: value,
        status_code: value,
        start: value,
        end: value,
        page: value,
        page_size: value,
      }).toString();

      const view = render(<APILogsPage />);

      expect(state.logQueries.at(-1)).toEqual({ page: 1, page_size: 10 });
      view.unmount();
    },
  );

  it.each(["0", "100", "999"])("accepts backend-supported status %s", (value) => {
    state.query = `status_code=${value}`;

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toMatchObject({ status_code: Number(value) });
  });

  it.each(["1", "99", "1000"])("omits backend-unsupported status %s", (value) => {
    state.query = `status_code=${value}`;

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).not.toHaveProperty("status_code");
  });

  it("changes Service and clears Route, Upstream, and page with one atomic URL patch", async () => {
    state.query = "api_service_id=7&api_route_id=9&api_upstream_id=11&page=3";
    render(<APILogsPage />);
    state.replace.mockClear();

    fireEvent.click(entityButton("api-service"));

    expect(state.replace).toHaveBeenCalledTimes(1);
    expect(state.replace).toHaveBeenCalledWith("/api-logs?api_service_id=8");
  });

  it("disables Route and Upstream pickers until a valid Service is selected", async () => {
    render(<APILogsPage />);

    openAdvancedFilters();

    expect(entityButton("api-route")).toBeDisabled();
    expect(entityButton("api-upstream")).toBeDisabled();
  });

  // behavior change: API log end is the next local midnight because the backend uses end-exclusive filtering.
  it("writes DateRangePicker bounds as exclusive Unix seconds without rendering Unix inputs", async () => {
    render(<APILogsPage />);

    expect(screen.queryByPlaceholderText("unixSeconds")).not.toBeInTheDocument();
    openAdvancedFilters();
    fireEvent.click(screen.getByRole("button", { name: "dateRange" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/api-logs?start=1767225600&end=1767312000",
    );
  });

  it("restores the fixed default seven-day range after an explicit URL range is cleared", () => {
    const mountedAt = new Date(2026, 6, 20, 12, 0, 0);
    vi.setSystemTime(mountedAt);
    const defaultEnd = Math.floor(mountedAt.getTime() / 1_000);
    const defaultStart = defaultEnd - 7 * 86_400;
    state.query = "start=1000&end=2000";
    const view = render(<APILogsPage />);
    expect(state.logQueries.at(-1)).toMatchObject({ start: 1_000, end: 2_000 });

    openAdvancedFilters();
    act(() => state.onDateRangeChange?.({ startDate: "", endDate: "" }));
    expect(state.replace).toHaveBeenLastCalledWith("/api-logs");

    state.query = "";
    view.rerender(<APILogsPage />);
    expect(state.logQueries.at(-1)).toMatchObject({ start: defaultStart, end: defaultEnd });
  });

  it("round-trips status zero as a string URL value and a numeric API filter", () => {
    render(<APILogsPage />);

    fireEvent.change(screen.getByRole("textbox", { name: "statusCode" }), {
      target: { value: "0" },
    });
    act(() => vi.advanceTimersByTime(300));

    expect(state.replace).toHaveBeenLastCalledWith("/api-logs?status_code=0");
  });

  it("restores the same filters and page after refresh or history navigation", () => {
    state.query = "request_id=req-a&page=2";
    const view = render(<APILogsPage />);
    expect(state.logQueries.at(-1)).toMatchObject({ request_id: "req-a", page: 2 });

    state.query = "request_id=req-b&api_service_id=7&page=4&page_size=20";
    view.rerender(<APILogsPage />);

    expect(state.logQueries.at(-1)).toMatchObject({
      request_id: "req-b",
      api_service_id: 7,
      page: 4,
      page_size: 20,
    });
  });
});
