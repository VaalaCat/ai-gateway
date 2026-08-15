import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { keepPreviousData } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import { dateStrToExclusiveEndTs, dateStrToTs } from "@/lib/utils/date-range";
import APILogsPage from "./page";

type DateRangePickerProps = {
  value: { startDate: string; endDate: string };
  onValueChange: (value: { startDate: string; endDate: string }) => void;
};

const state = vi.hoisted(() => ({
  isAdmin: true,
  query: "",
  replace: vi.fn(),
  capability: {} as Record<string, unknown>,
  logQueries: [] as Array<Record<string, unknown>>,
  logOptions: [] as Array<Record<string, unknown> | undefined>,
  logScopes: [] as string[],
  logs: {} as Record<string, unknown>,
}));

vi.mock("next/navigation", () => ({
  usePathname: () => "/api-logs",
  useRouter: () => ({ replace: state.replace }),
  useSearchParams: () => new URLSearchParams(state.query),
}));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 }, isAdmin: state.isAdmin }) }));
vi.mock("@/lib/api/capabilities", () => ({ useCapabilities: () => state.capability }));
vi.mock("@/lib/api/api-logs", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-logs")>()),
  useAPIRequestLogs: (query: Record<string, unknown>, scope: string, options?: Record<string, unknown>) => {
    state.logQueries.push(query);
    state.logScopes.push(scope);
    state.logOptions.push(options);
    return state.logs;
  },
  useAPIRequestTrace: () => ({ data: undefined, error: null, isLoading: false }),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, entity, disabled }: { id?: string; entity: string; disabled?: boolean }) => (
    <button id={id} type="button" disabled={disabled}>{entity}</button>
  ),
}));
vi.mock("@/components/business/date-picker/date-range-picker", () => ({
  DateRangePicker: (props: DateRangePickerProps) => {
    return (
      <button
        type="button"
        onClick={() => props.onValueChange({ startDate: "2026-08-01", endDate: "2026-08-03" })}
      >
        dateRange
      </button>
    );
  },
}));
vi.mock("./_components/trace-dialog", () => ({
  TraceDialog: ({ request, onOpenChange }: { request: typeof log | null; onOpenChange: (open: boolean) => void }) => request ? (
    <div role="dialog" aria-label="selected trace">
      <output aria-label="selected request id">{request.request_id}</output>
      <button type="button" onClick={() => onOpenChange(false)}>close trace</button>
    </div>
  ) : null,
}));
vi.mock("./_components/request-details", () => ({
  APIRequestDetails: ({ request }: { request: typeof log }) => <div>details:{request.request_id}</div>,
}));

const log = {
  request_id: "req-abcdefghijklmnopqrstuvwxyz-0123456789",
  api_service_id: 7,
  api_service_name: "Deleted weather service snapshot with a very long name",
  api_route_id: 9,
  api_route_name: "Forecast route snapshot",
  api_upstream_id: 11,
  api_upstream_name: "Primary upstream snapshot",
  token_id: 12,
  token_name: "production-token",
  protocol: "http",
  method: "POST",
  status_code: 200,
  duration_ms: 42,
  created_at: 1_000,
};

function enabledCapability() {
  return {
    data: { generic_api: { logs: true } },
    error: null,
    isPending: false,
    isLoading: false,
  };
}

function page(data = [log], total = data.length) {
  return {
    data: { data, total, page: 1, page_size: 10 },
    error: null,
    isLoading: false,
    isFetching: false,
    refetch: vi.fn(),
  };
}

describe("APILogsPage list explorer", () => {
  beforeEach(() => {
    state.isAdmin = true;
    state.query = "";
    state.replace.mockReset();
    state.capability = enabledCapability();
    state.logQueries = [];
    state.logOptions = [];
    state.logScopes = [];
    state.logs = page();
    window.localStorage.clear();
  });

  it("uses the current-user log scope", () => {
    state.isAdmin = false;
    render(<APILogsPage />);
    expect(state.logScopes.at(-1)).toBe("portal");
  });

  it("keeps the page-level date range visible and out of advanced filters", async () => {
    const user = userEvent.setup();
    render(<APILogsPage />);

    expect(screen.getByRole("button", { name: "dateRange" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: /filters/ }));
    expect(screen.getAllByRole("button", { name: "dateRange" })).toHaveLength(1);
  });

  it("updates the API log query with an exclusive end when the header range changes", async () => {
    const user = userEvent.setup();
    render(<APILogsPage />);

    await user.click(screen.getByRole("button", { name: "dateRange" }));

    const nextURL = new URL(String(state.replace.mock.calls.at(-1)?.[0]), "http://localhost");
    expect(nextURL.searchParams.get("start")).toBe(String(dateStrToTs("2026-08-01", false)));
    expect(nextURL.searchParams.get("end")).toBe(String(dateStrToExclusiveEndTs("2026-08-03")));
  });

  it("keeps one page refresh and exactly three table actions", async () => {
    const user = userEvent.setup();
    const refetch = vi.fn();
    state.logs = { ...page(), refetch };
    const { container } = render(<APILogsPage />);

    const refresh = screen.getByRole("button", { name: "Refresh" });
    expect(screen.getAllByRole("button", { name: "Refresh" })).toHaveLength(1);
    expect(container.querySelectorAll('[data-slot="toolbar-actions"] button')).toHaveLength(3);

    await user.click(refresh);
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("shows a capability skeleton while pending without flashing unavailable", () => {
    state.capability = { data: undefined, error: null, isPending: true, isLoading: true };

    const { container } = render(<APILogsPage />);

    expect(container.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
    expect(screen.queryByText("unavailable")).not.toBeInTheDocument();
    expect(state.logOptions.at(-1)).toMatchObject({ enabled: false });
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("distinguishes explicit capability unavailability from a capability error", () => {
    state.capability = { data: { generic_api: { logs: false } }, error: null, isPending: false, isLoading: false };
    const unavailable = render(<APILogsPage />);
    expect(screen.getByRole("alert")).toHaveTextContent("unavailable");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    unavailable.unmount();

    state.capability = { data: undefined, error: new Error("capability failed"), isPending: false, isLoading: false };
    render(<APILogsPage />);
    expect(screen.getByRole("alert")).toHaveTextContent("loadFailed");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it.each([
    [403, undefined, "permissionDenied"],
    [400, undefined, "invalidFilters"],
    [503, "LogDatabaseUnavailable", "logUnavailable"],
    [503, "OtherUnavailable", "loadFailed"],
    [500, undefined, "loadFailed"],
  ])("maps list error %s/%s to %s before considering empty data", (status, code, message) => {
    state.logs = {
      data: undefined,
      error: new ApiError(status, "list failed", code ? { code } : undefined),
      isLoading: false,
      isFetching: false,
    };

    render(<APILogsPage />);

    expect(screen.getByRole("alert")).toHaveTextContent(message);
    expect(screen.queryByText("noData")).not.toBeInTheDocument();
  });

  it("renders a successful empty page as data rather than an error", () => {
    state.logs = page([], 0);

    render(<APILogsPage />);

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("noData")).toBeInTheDocument();
  });

  it("passes URL filters and server pagination to the query and DataTable", () => {
    state.query = "request_id=req-1&api_service_id=7&page=2&page_size=20";
    state.logs = {
      ...page([log], 41),
      data: { data: [log], total: 41, page: 2, page_size: 20 },
    };

    render(<APILogsPage />);

    expect(state.logQueries.at(-1)).toMatchObject({
      request_id: "req-1",
      api_service_id: 7,
      page: 2,
      page_size: 20,
    });
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
  });

  it("keeps placeholder rows visible during background fetching", () => {
    state.logs = { ...page(), isFetching: true };

    const { container } = render(<APILogsPage />);

    expect(screen.getByText(log.api_service_name)).toBeInTheDocument();
    expect(container.querySelector('[data-slot="skeleton"]')).not.toBeInTheDocument();
    expect(state.logOptions.at(-1)?.placeholderData).toBe(keepPreviousData);
  });

  it("keeps automatic refresh off by default and restores a persisted interval", () => {
    render(<APILogsPage />);
    expect(state.logOptions.at(-1)).toMatchObject({ refetchInterval: false });

    window.localStorage.setItem("aigw:pref:1:api-logs-auto-refresh", "10000");
    state.logOptions = [];
    render(<APILogsPage />);
    expect(state.logOptions.at(-1)).toMatchObject({ refetchInterval: 10_000 });
  });

  it("corrects a stale URL page to the last server page", () => {
    state.query = "page=9";
    state.logs = {
      ...page([], 21),
      data: { data: [], total: 21, page: 9, page_size: 10 },
    };

    render(<APILogsPage />);

    expect(state.replace).toHaveBeenLastCalledWith("/api-logs?page=3");
  });

  it("corrects a stale URL page when the filtered result becomes empty", () => {
    state.query = "page=4";
    state.logs = {
      ...page([], 0),
      data: { data: [], total: 0, page: 4, page_size: 10 },
    };

    render(<APILogsPage />);

    expect(state.replace).toHaveBeenLastCalledWith("/api-logs");
  });

  it("shows snapshot identity and zero status without replacing names from management entities", () => {
    state.logs = page([{ ...log, status_code: 0 }]);

    render(<APILogsPage />);

    expect(screen.getByText(log.api_service_name)).toBeInTheDocument();
    expect(screen.getByText("noResponse")).toBeInTheDocument();
    expect(document.querySelector('[data-slot="api-http-status-badge"]')).toHaveAttribute("data-state", "unavailable");
  });

  it("keeps secondary log fields available through column visibility", async () => {
    const user = userEvent.setup();
    render(<APILogsPage />);

    await user.click(screen.getByRole("button", { name: "columns" }));

    for (const label of ["route", "upstream", "token", "protocol", "method", "duration", "createdAt"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
  });

  it("contains long values inside the table scroll region", () => {
    render(<APILogsPage />);

    const serviceName = screen.getByText(log.api_service_name);
    expect(serviceName).toHaveClass("truncate");
    expect(serviceName.closest("div.overflow-x-auto")).not.toBeNull();
  });

  it("expands one request into the shared detail list", () => {
    render(<APILogsPage />);

    fireEvent.click(screen.getByRole("button", { name: "expandDetails" }));

    expect(screen.getByText(`details:${log.request_id}`)).toBeInTheDocument();
  });

  it("opens the complete raw log JSON without changing the filter URL", () => {
    render(<APILogsPage />);

    fireEvent.click(screen.getByRole("button", { name: "viewRawJson" }));

    expect(screen.getByRole("dialog")).toHaveTextContent(log.request_id);
    expect(screen.getByRole("dialog")).toHaveTextContent(log.api_route_name);
    expect(state.replace).not.toHaveBeenCalled();
  });
});
