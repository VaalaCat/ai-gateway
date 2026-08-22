import { useSyncExternalStore } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute } from "@/lib/api/api-services";
import { ApiError } from "@/lib/api/client";
import APIServiceDetailPage, { patchRouteTableURL, readRouteTableURLState } from "./page";

const route = (overrides: Partial<APIRoute> = {}): APIRoute => ({
  id: 9, api_service_id: 7, backend_id: 17, slug: "forecast", protocols: ["http"],
  allowed_methods: ["GET"], upstream_path: "forecast", forward_subpath: false,
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
  status: 1, ...overrides,
});
const target: APIBackend = { id: 17, api_service_id: 7, name: "Primary Target", route_count: 1, upstream_count: 1, enabled_upstream_count: 1, endpoint_hosts: ["example.test"] };
const page = (data: APIRoute[], total = data.length, current = 1, pageSize = 10) => ({ data, total, page: current, page_size: pageSize, api_service_id: 7 });

const state = vi.hoisted(() => ({
  searchParams: new URLSearchParams("id=7"), navigationRevision: 0,
  capability: { data: { generic_api: { services: true, access: true, logs: true, service_actions: { create: true, manage_all: true, manage_ids: [] as number[] } } } } as Record<string, unknown>,
  service: { data: { id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 100000, status: 1 }, error: null, isLoading: false } as Record<string, unknown>,
  routes: { data: { data: [] as APIRoute[], total: 0, page: 1, page_size: 10, api_service_id: 7 }, error: null, isLoading: false, isFetching: false, isPlaceholderData: false, refetch: vi.fn() } as Record<string, unknown>,
}));
const navigation = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn() }));
const hooks = vi.hoisted(() => ({
  service: vi.fn(), routes: vi.fn(), routeParams: undefined as Record<string, unknown> | undefined,
  routeQueryCalls: [] as Record<string, unknown>[], deleteService: vi.fn(), deleteRoute: vi.fn(), deleteBackend: vi.fn(), deleteUpstream: vi.fn(),
  previewCalls: [] as unknown[][],
  download: vi.fn(),
  document: vi.fn(),
}));
interface TableProps {
  routes: APIRoute[];
  expandedRouteID?: number;
  canManage: boolean;
  error?: unknown;
  requestedPage: number;
  displayedPage: number;
  displayedPageSize: number;
  isPlaceholderData: boolean;
  refreshing: boolean;
  onRetry: () => void;
  onFiltersChange: (patch: { search?: string }) => void;
  onPaginationChange: (page: number, pageSize: number) => void;
  onExpandedRouteChange: (routeID?: number) => void;
  onDeleteRoute: (route: APIRoute) => void;
  renderExpandedRoute: (route: APIRoute) => React.ReactNode;
}
const table = vi.hoisted(() => ({ props: undefined as TableProps | undefined }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string, values?: Record<string, unknown>) => values?.count === undefined && values?.subject === undefined ? key : `${key}:${values.count ?? values.subject}` }));
vi.mock("next/navigation", () => ({
  useRouter: () => navigation,
  useSearchParams: () => {
    const snapshot = useSyncExternalStore((notify) => {
      const update = () => { state.searchParams = new URLSearchParams(window.location.search); state.navigationRevision += 1; notify(); };
      window.addEventListener("popstate", update); return () => window.removeEventListener("popstate", update);
    }, () => `${state.navigationRevision}:${state.searchParams.toString()}`, () => "0:");
    return new URLSearchParams(snapshot.slice(snapshot.indexOf(":") + 1));
  },
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({ ...(await importOriginal<typeof import("@/lib/api/capabilities")>()), useCapabilities: () => state.capability }));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIService: hooks.service,
  useAPIRoutes: (params: Record<string, unknown>) => { hooks.routeParams = params; hooks.routeQueryCalls.push(params); return state.routes; },
  useDeleteAPIService: () => ({ mutateAsync: hooks.deleteService }),
  useDeleteAPIRoute: () => ({ mutateAsync: hooks.deleteRoute }),
  useDeleteAPIBackend: () => ({ mutateAsync: hooks.deleteBackend }),
  useDeleteAPIUpstream: () => ({ mutateAsync: hooks.deleteUpstream }),
  useAPIRoutePreview: (...args: unknown[]) => { hooks.previewCalls.push(args); return { data: undefined, isLoading: false, error: null, refetch: vi.fn() }; },
  downloadServiceOpenAPI: hooks.download,
  useGetOpenAPIDocument: hooks.document,
}));
vi.mock("../_components/route-table/route-data-table", () => ({
  RouteDataTable: (props: TableProps) => {
    table.props = props;
    return <div data-testid="route-table"><button onClick={() => props.onFiltersChange({ search: "radar" })}>search</button><button onClick={() => props.onPaginationChange(2, 20)}>page</button>{props.routes.map((item: APIRoute) => <div key={item.id}><span>{item.slug}</span><button onClick={() => props.onExpandedRouteChange(props.expandedRouteID === item.id ? undefined : item.id)}>{props.expandedRouteID === item.id ? `collapse ${item.slug}` : `expand ${item.slug}`}</button>{props.canManage ? <button onClick={() => props.onDeleteRoute(item)}>delete {item.slug}</button> : null}{props.expandedRouteID === item.id ? props.renderExpandedRoute(item) : null}</div>)}</div>;
  },
}));
vi.mock("../_components/route-table/route-expanded-workspace", () => ({
  RouteExpandedWorkspace: ({ route, canManage, onDeleteTarget, onDeleteEndpoint }: { route: APIRoute; canManage: boolean; onDeleteTarget: (target: APIBackend) => void; onDeleteEndpoint: (endpoint: { id: number }) => void }) => <div data-testid={`route-expanded-workspace-${route.id}`}>Primary Target{canManage ? <><button onClick={() => onDeleteTarget(target)}>delete target</button><button onClick={() => onDeleteEndpoint({ id: 31 })}>delete endpoint</button></> : null}<span>{route.slug}</span></div>,
}));

function setURL(search: string) { state.searchParams = new URLSearchParams(search); window.history.replaceState({}, "", `/api-services/detail?${search}`); }
function renderPage() { return render(<APIServiceDetailPage />); }
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((success, failure) => { resolve = success; reject = failure; });
  return { promise, resolve, reject };
}

describe("Route table URL state", () => {
  it("normalizes invalid numbers and accepts only legal status/page size values", () => {
    expect(readRouteTableURLState(new URLSearchParams("route_search=x&route_status=2&route_page=-3&route_page_size=25&route=0"))).toEqual({ search: "x", page: 1, pageSize: 10 });
    expect(readRouteTableURLState(new URLSearchParams("route_status=0&route_page=2&route_page_size=50&route=9"))).toEqual({ search: "", status: 0, page: 2, pageSize: 50, routeID: 9 });
  });

  it("writes a stable workspace query and omits default values", () => {
    expect(patchRouteTableURL(7, { search: "daily report/东京", status: 1, page: 1, pageSize: 10, routeID: 9 })).toBe("/api-services/detail?id=7&route_search=daily+report%2F%E4%B8%9C%E4%BA%AC&route_status=1&route=9");
  });
});

describe("APIServiceDetailPage", () => {
  beforeEach(() => {
    setURL("id=7"); state.navigationRevision = 0;
    state.capability = { data: { generic_api: { services: true, access: true, logs: true, service_actions: { create: true, manage_all: true, manage_ids: [] } } } };
    state.routes = { data: page([route()]), error: null, isLoading: false, isFetching: false, isPlaceholderData: false, refetch: vi.fn() };
    hooks.service.mockReset(); hooks.service.mockImplementation(() => state.service); hooks.routeParams = undefined; hooks.routeQueryCalls = []; hooks.previewCalls = [];
    for (const mutation of [hooks.deleteService, hooks.deleteRoute, hooks.deleteBackend, hooks.deleteUpstream]) { mutation.mockReset(); mutation.mockResolvedValue(undefined); }
    hooks.download.mockReset(); hooks.download.mockResolvedValue(undefined);
    hooks.document.mockReset(); hooks.document.mockReturnValue({ data: { service: { document: {} }, routes: [] } });
    navigation.replace.mockReset(); navigation.push.mockReset(); table.props = undefined;
  });

  it("queries the requested Route page while displaying the response snapshot", () => {
    setURL("id=7&route_search=forecast&route_status=1&route_page=2&route_page_size=20");
    state.routes = { ...state.routes, data: page([route({ id: 10 })], 24, 1, 10), isFetching: true, isPlaceholderData: true };
    renderPage();
    expect(hooks.routeParams).toEqual({ api_service_id: 7, search: "forecast", status: 1, page: 2, page_size: 20 });
    expect(table.props).toEqual(expect.objectContaining({ requestedPage: 2, displayedPage: 1, displayedPageSize: 10, isPlaceholderData: true, refreshing: true }));
  });

  it("offers an OpenAPI JSON export in the normal service action area", async () => {
    renderPage();
    await userEvent.setup().click(screen.getByRole("button", { name: "exportOpenAPI" }));
    expect(hooks.download).toHaveBeenCalledWith(7, "weather");
  });

  it("offers the document editor only for a service that has an OpenAPI document", () => {
    renderPage();
    expect(screen.queryByRole("link", { name: "editOpenAPIDocument" })).not.toBeInTheDocument();
    hooks.document.mockReturnValue({ data: { service: { document: { openapi: "3.1.0" } }, routes: [] } });
    renderPage();
    expect(screen.getByRole("link", { name: "editOpenAPIDocument" })).toHaveAttribute("href", "/api-services/openapi?id=7");
  });

  it.each([
    { data: undefined, isLoading: true, error: null },
    { data: undefined, isLoading: false, error: new Error("offline") },
  ])("keeps the document editor entry fail-closed while its document query is unavailable: %#", (document) => {
    hooks.document.mockReturnValue(document);
    renderPage();
    expect(screen.queryByRole("link", { name: "editOpenAPIDocument" })).not.toBeInTheDocument();
  });

  it("uses a local export fallback for an unknown API error", async () => {
    hooks.download.mockRejectedValueOnce(new ApiError(500, "untrusted export wording", { code: "future_code" }));
    renderPage();
    await userEvent.setup().click(screen.getByRole("button", { name: "exportOpenAPI" }));
    expect(await screen.findByText("openAPIExportFailed")).toBeVisible();
    expect(screen.queryByText("untrusted export wording")).not.toBeInTheDocument();
  });

  it("keeps placeholder rows visible without mounting the previous page Route workspace", () => {
    setURL("id=7&route_page=2&route=9");
    state.routes = { ...state.routes, data: page([route()], 2, 1, 10), isFetching: true, isPlaceholderData: true };
    const view = renderPage();

    expect(screen.getByRole("button", { name: "expand forecast" })).toBeVisible();
    expect(screen.queryByText("Primary Target")).not.toBeInTheDocument();
    expect(table.props?.expandedRouteID).toBeUndefined();

    state.routes = { ...state.routes, data: page([route({ id: 10, slug: "radar" })], 2, 2, 10), isFetching: false, isPlaceholderData: false };
    view.rerender(<APIServiceDetailPage />);
    expect(screen.getByText("routeOutsideCurrentPage")).toBeVisible();
    expect(screen.queryByText("Primary Target")).not.toBeInTheDocument();
  });

  it("does not expand a response whose page size does not match the requested snapshot", () => {
    setURL("id=7&route_page_size=20&route=9");
    state.routes = { ...state.routes, data: page([route()], 1, 1, 10), isFetching: false, isPlaceholderData: false };
    renderPage();

    expect(table.props?.expandedRouteID).toBeUndefined();
    expect(screen.queryByText("Primary Target")).not.toBeInTheDocument();
    expect(screen.queryByText("routeOutsideCurrentPage")).not.toBeInTheDocument();
  });

  it("stores expansion in the URL and restores it without scanning another page", async () => {
    setURL("id=7&route=9"); renderPage();
    expect(screen.getByText("Primary Target")).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "collapse forecast" }));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7");
    expect(hooks.routeQueryCalls).toHaveLength(1);
  });

  it("does not invoke legacy hover preview or render a routing dialog for an inline Route workspace", async () => {
    setURL("id=7&route=9");
    renderPage();

    await userEvent.setup().hover(screen.getAllByText("forecast")[0]!);

    expect(hooks.previewCalls).toHaveLength(0);
    expect(screen.getByTestId("route-expanded-workspace-9")).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows an out-of-result notice and clears filters without scanning pages", async () => {
    setURL("id=7&route_search=forecast&route_status=1&route_page=2&route=9"); state.routes = { ...state.routes, data: page([route({ id: 10 })], 20, 2, 10) };
    renderPage();
    expect(screen.getByText("routeOutsideCurrentPage")).toBeVisible(); expect(hooks.routeQueryCalls).toHaveLength(1);
    await userEvent.setup().click(screen.getByRole("button", { name: "clearRouteFilters" }));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7");
  });

  it("resets page and expansion for filters, and clears expansion on pagination", async () => {
    setURL("id=7&route_search=old&route_page=3&route_page_size=10&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "search" }));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7&route_search=radar");
    await user.click(screen.getByRole("button", { name: "page" }));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7&route_search=old&route_page=2&route_page_size=20");
  });

  it("reacts to browser back/forward and rejects a Route from another Service", () => {
    const view = renderPage();
    act(() => { window.history.replaceState({}, "", "/api-services/detail?id=7&route_search=back&route=9"); window.dispatchEvent(new PopStateEvent("popstate")); });
    expect(hooks.routeParams).toEqual(expect.objectContaining({ search: "back" }));
    state.routes = { ...state.routes, data: page([route({ api_service_id: 8 })]) }; view.rerender(<APIServiceDetailPage />);
    expect(screen.getByText("routeOutsideCurrentPage")).toBeVisible();
  });

  it("keeps the PageHeader and retry available when the Route query fails", async () => {
    setURL("id=7&route=9");
    const refetch = vi.fn(); state.routes = { ...state.routes, data: undefined, error: new Error("offline"), refetch };
    renderPage(); expect(screen.getByRole("heading", { level: 1, name: "Weather" })).toBeVisible();
    expect(screen.queryByText("routeOutsideCurrentPage")).not.toBeInTheDocument();
    expect(table.props?.error).toBeInstanceOf(Error); table.props?.onRetry(); expect(refetch).toHaveBeenCalledOnce();
  });

  it("confirms Route deletion, clears its URL, and restores stable heading focus", async () => {
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete forecast" })); await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    await waitFor(() => expect(hooks.deleteRoute).toHaveBeenCalledWith(9));
    expect(navigation.replace).toHaveBeenCalledWith("/api-services/detail?id=7");
    await waitFor(() => expect(screen.getByRole("heading", { name: "routesLabel" })).toHaveFocus());
  });

  it("keeps Route confirmation, URL, and focus unchanged when deletion rejects", async () => {
    hooks.deleteRoute.mockRejectedValue(new Error("route delete rejected"));
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete forecast" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent("route delete rejected");
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(screen.getByText("routesLabel")).not.toHaveFocus();
  });

  it("returns to the last valid page and clears expansion after deleting the only Route on page two", async () => {
    setURL("id=7&route_page=2&route=9");
    state.routes = { ...state.routes, data: page([route()], 11, 2, 10) };
    const view = renderPage();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "delete forecast" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    await waitFor(() => expect(hooks.deleteRoute).toHaveBeenCalledWith(9));

    state.routes = { ...state.routes, data: page([], 10, 2, 10), isFetching: false, isPlaceholderData: false };
    view.rerender(<APIServiceDetailPage />);

    await waitFor(() => expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7"));
  });

  it("does not change page when deleting the only Route on page two fails", async () => {
    hooks.deleteRoute.mockRejectedValue(new Error("route delete rejected"));
    setURL("id=7&route_page=2&route=9");
    state.routes = { ...state.routes, data: page([route()], 11, 2, 10) };
    renderPage();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "delete forecast" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent("route delete rejected");
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("does not converge a stale response from a different requested page", async () => {
    setURL("id=7&route_page=2&route=9");
    state.routes = { ...state.routes, data: page([], 10, 1, 10), isFetching: false, isPlaceholderData: false };
    renderPage();

    await waitFor(() => expect(table.props).toEqual(expect.objectContaining({ requestedPage: 2, displayedPage: 1 })));
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("converges the same invalid last page again after that page becomes valid", async () => {
    setURL("id=7&route_page=2");
    state.routes = { ...state.routes, data: page([], 10, 2, 10), isFetching: false, isPlaceholderData: false };
    const view = renderPage();

    await waitFor(() => expect(navigation.replace).toHaveBeenCalledTimes(1));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7");

    state.routes = { ...state.routes, data: page([route()], 11, 2, 10), isFetching: false, isPlaceholderData: false };
    act(() => {
      window.history.replaceState({}, "", "/api-services/detail?id=7&route_page=2");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    view.rerender(<APIServiceDetailPage />);
    expect(table.props).toEqual(expect.objectContaining({ requestedPage: 2, displayedPage: 2 }));

    state.routes = { ...state.routes, data: page([], 10, 2, 10), isFetching: false, isPlaceholderData: false };
    view.rerender(<APIServiceDetailPage />);

    await waitFor(() => expect(navigation.replace).toHaveBeenCalledTimes(2));
    expect(navigation.replace).toHaveBeenLastCalledWith("/api-services/detail?id=7");
  });

  it("ignores a pending Route completion after browser navigation changes location", async () => {
    const pending = deferred<void>();
    hooks.deleteRoute.mockReturnValue(pending.promise);
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete forecast" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    act(() => { window.history.replaceState({}, "", "/api-services/detail?id=7&route_search=radar&route=10"); window.dispatchEvent(new PopStateEvent("popstate")); });
    pending.resolve();

    await waitFor(() => expect(hooks.deleteRoute).toHaveBeenCalledWith(9));
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(screen.getByText("routesLabel")).not.toHaveFocus();
  });

  it("does not let an older completion close or publish over a newer delete operation", async () => {
    const first = deferred<void>();
    hooks.deleteRoute.mockReturnValueOnce(first.promise).mockResolvedValueOnce(undefined);
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete forecast" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    act(() => table.props?.onDeleteRoute(route({ id: 10, slug: "radar" })));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("radar");

    first.resolve();
    await waitFor(() => expect(screen.getByRole("alertdialog")).toHaveTextContent("radar"));
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(screen.getByText("routesLabel")).not.toHaveFocus();
  });

  it("deletes a Target, clears only its affected Route, and restores heading focus", async () => {
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete target" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    await waitFor(() => expect(hooks.deleteBackend).toHaveBeenCalledWith(17));
    expect(navigation.replace).toHaveBeenCalledWith("/api-services/detail?id=7");
    await waitFor(() => expect(screen.getByRole("heading", { name: "routesLabel" })).toHaveFocus());
  });

  it("keeps Target confirmation and Route URL when deletion fails generally", async () => {
    hooks.deleteBackend.mockRejectedValue(new Error("target delete rejected"));
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete target" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent("target delete rejected");
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(screen.getByText("routesLabel")).not.toHaveFocus();
  });

  it("maps Target impact errors and keeps the confirmation open", async () => {
    hooks.deleteBackend.mockRejectedValue(new ApiError(409, "conflict", { code: "backend_in_use", details: { route_count: 3 } }));
    setURL("id=7&route=9"); renderPage(); const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "delete target" })); expect(screen.getByRole("alertdialog")).toHaveTextContent("deleteTargetDescription:Primary Target");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("backendInUse:1");
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("backendInUse:3");
  });

  it("delegates Endpoint deletion to its expanded workspace mutation", async () => {
    setURL("id=7&route=9"); renderPage(); await userEvent.setup().click(screen.getByRole("button", { name: "delete endpoint" }));
    expect(hooks.deleteUpstream).toHaveBeenCalledWith(31);
  });

  it("renders no management entry in readonly mode", () => {
    state.capability = { data: { generic_api: { services: true, service_actions: { create: false, manage_all: false, manage_ids: [] } } } };
    setURL("id=7&route=9"); renderPage();
    expect(screen.queryByRole("button", { name: /delete/ })).not.toBeInTheDocument();
    expect(table.props?.canManage).toBe(false);
  });
});
