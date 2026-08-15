import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import APILogsPage from "./page";

const state = vi.hoisted(() => ({ searchParams: new URLSearchParams() }));
const navigation = vi.hoisted(() => ({ replace: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  usePathname: () => "/api-logs",
  useRouter: () => navigation,
  useSearchParams: () => state.searchParams,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", () => ({
  useCapabilities: () => ({
    data: { generic_api: { logs: true } },
    isLoading: false,
    isPending: false,
    error: null,
  }),
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("@/components/business/date-picker/date-range-picker", () => ({
  DateRangePicker: () => <button type="button">dateRange</button>,
}));
vi.mock("./_components/trace-dialog", () => ({ TraceDialog: () => null }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({
    data,
    total,
    page,
    pageSize,
    pageCount,
  }: {
    data: Array<{ request_id: string }>;
    total: number;
    page: number;
    pageSize: number;
    pageCount: number;
  }) => (
    <div
      data-testid="table"
      data-total={total}
      data-page={page}
      data-page-size={pageSize}
      data-page-count={pageCount}
    >
      {data.map((row) => <span key={row.request_id}>{row.request_id}</span>)}
    </div>
  ),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function log(requestID: string) {
  return {
    request_id: requestID,
    api_service_id: 7,
    api_service_name: "Weather",
    api_route_id: 9,
    api_route_name: "Forecast",
    api_upstream_id: 11,
    api_upstream_name: "Primary",
    token_id: 12,
    token_name: "production",
    protocol: "http",
    method: "GET",
    status_code: 200,
    duration_ms: 5,
    created_at: 1_000,
  };
}

function page(requestID: string, total: number, currentPage: number, pageSize: number) {
  return {
    data: requestID ? [log(requestID)] : [],
    total,
    page: currentPage,
    page_size: pageSize,
  };
}

function renderPage() {
  const queryClient = createTestQueryClient();
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <APILogsPage />
      </QueryClientProvider>,
    ),
  };
}

function rerenderPage(view: ReturnType<typeof renderPage>) {
  view.rerender(
    <QueryClientProvider client={view.queryClient}>
      <APILogsPage />
    </QueryClientProvider>,
  );
}

describe("APILogsPage server pagination truth", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams();
    navigation.replace.mockReset();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => vi.unstubAllGlobals());

  it("keeps old small-page truth while a larger page is pending and restores cached truth on back", async () => {
    const next = deferred<Response>();
    const back = deferred<Response>();
    let firstPageReads = 0;
    vi.mocked(fetch).mockImplementation((input) => {
      const params = new URL(String(input), "http://local").searchParams;
      if (params.get("page") === "2") return next.promise;
      firstPageReads += 1;
      return firstPageReads === 1
        ? Promise.resolve(json(page("old-small", 1, 1, 10)))
        : back.promise;
    });

    const view = renderPage();
    await screen.findByText("old-small");

    state.searchParams = new URLSearchParams("page=2");
    rerenderPage(view);

    expect(screen.getByText("old-small")).toBeInTheDocument();
    expect(screen.getByTestId("table")).toHaveAttribute("data-page", "1");
    expect(screen.getByTestId("table")).toHaveAttribute("data-page-count", "1");
    expect(navigation.replace).not.toHaveBeenCalled();

    next.resolve(json(page("new-large", 41, 2, 10)));
    await screen.findByText("new-large");
    expect(screen.getByTestId("table")).toHaveAttribute("data-page", "2");
    expect(screen.getByTestId("table")).toHaveAttribute("data-page-count", "5");

    state.searchParams = new URLSearchParams();
    rerenderPage(view);
    expect(screen.getByText("old-small")).toBeInTheDocument();
    expect(screen.getByTestId("table")).toHaveAttribute("data-page", "1");
    expect(navigation.replace).not.toHaveBeenCalled();
    back.resolve(json(page("old-small", 1, 1, 10)));
  });

  it("does not clamp a large cached page until the smaller real response arrives", async () => {
    const smaller = deferred<Response>();
    vi.mocked(fetch)
      .mockResolvedValueOnce(json(page("old-large", 41, 4, 10)))
      .mockImplementationOnce(() => smaller.promise);
    state.searchParams = new URLSearchParams("request_id=old&page=4");
    const view = renderPage();
    await screen.findByText("old-large");

    state.searchParams = new URLSearchParams("request_id=new&page=4");
    rerenderPage(view);
    expect(screen.getByText("old-large")).toBeInTheDocument();
    expect(navigation.replace).not.toHaveBeenCalled();

    smaller.resolve(json(page("", 1, 4, 10)));
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-logs?request_id=new"));
  });

  it("keeps placeholder rows and never rewrites the URL when the new query errors", async () => {
    const failed = deferred<Response>();
    vi.mocked(fetch)
      .mockResolvedValueOnce(json(page("cached", 41, 4, 10)))
      .mockImplementationOnce(() => failed.promise);
    state.searchParams = new URLSearchParams("request_id=old&page=4");
    const view = renderPage();
    await screen.findByText("cached");

    state.searchParams = new URLSearchParams("request_id=new&page=4");
    rerenderPage(view);
    expect(screen.getByText("cached")).toBeInTheDocument();
    expect(navigation.replace).not.toHaveBeenCalled();

    failed.resolve(json({ error: "log store unavailable" }, 503));
    expect(await screen.findByRole("alert")).toHaveTextContent("loadFailed");
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("atomically normalizes URL pagination to the successful server response", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(json(page("normalized", 100, 3, 20)));
    state.searchParams = new URLSearchParams("page=3&page_size=101");

    renderPage();
    await screen.findByText("normalized");

    expect(screen.getByTestId("table")).toHaveAttribute("data-page", "3");
    expect(screen.getByTestId("table")).toHaveAttribute("data-page-size", "20");
    expect(screen.getByTestId("table")).toHaveAttribute("data-page-count", "5");
    expect(navigation.replace).toHaveBeenCalledTimes(1);
    expect(navigation.replace).toHaveBeenCalledWith("/api-logs?page=3&page_size=20");
  });
});
