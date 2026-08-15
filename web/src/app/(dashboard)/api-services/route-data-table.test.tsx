import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute, APIService } from "@/lib/api/api-services";

import {
  buildRouteTableColumns,
  normalizeRouteTableSearch,
  RouteDataTable,
  type RouteDataTableProps,
} from "./_components/route-table/route-data-table";

const hooks = vi.hoisted(() => ({
  backends: new Map<number, APIBackend>(),
  errorIDs: new Set<number>(),
  loadingIDs: new Set<number>(),
  requestedIDs: [] as number[],
  breakpoint: "lg+" as "xs" | "sm-md" | "lg+",
}));

vi.mock("@/lib/api/api-services", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/api-services")>();
  return {
    ...actual,
    useAPIBackend: (id: number) => {
      hooks.requestedIDs.push(id);
      return {
        data: hooks.backends.get(id),
        error: hooks.errorIDs.has(id) ? new Error("target rejected") : null,
        isLoading: hooks.loadingIDs.has(id),
      };
    },
  };
});

vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));
vi.mock("@/lib/hooks/use-breakpoint", () => ({ useBreakpoint: () => hooks.breakpoint }));

const messages: Record<string, string> = {
  actions: "操作",
  all: "全部",
  allMethodsShort: "全部",
  allowedMethods: "方法",
  collapseRoute: "收起 forecast",
  copyClientRequest: "复制完整请求 URL",
  createRoute: "新建路由",
  deleteRoute: "删除路由",
  disabled: "禁用",
  editRoute: "编辑路由",
  enabled: "启用",
  expandRoute: "展开 forecast",
  nextPage: "下一页",
  noData: "暂无数据",
  previousPage: "上一页",
  protocolsAndStatus: "协议 / 状态",
  protocols: "协议",
  refreshingRoutes: "正在刷新路由",
  requestURL: "完整请求 URL",
  retry: "重试",
  routeActions: "Route 操作",
  routeSearch: "搜索 Route 或完整请求 URL",
  routesLoadFailed: "路由加载失败",
  routesLoadFailedDescription: "路由请求失败，不影响 Service 信息。",
  status: "状态",
  target: "目标",
  targetLoadFailed: "目标加载失败",
  targetLoading: "正在加载 Target",
  routes: "路由",
  total: "共 21 条",
  urlCopied: "URL 已复制",
};

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: { count?: number; route?: string }) => {
    if (key === "expandRoute" || key === "collapseRoute") {
      return `${key === "expandRoute" ? "展开" : "收起"} ${values?.route ?? "forecast"}`;
    }
    if (key === "endpointCount") return `${values?.count ?? 0} 个 Endpoint`;
    return messages[key] ?? key;
  },
}));

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const service: APIService = {
  id: 7,
  slug: "weather",
  name: "Weather",
  description: "Forecast service",
  price_per_call: 1,
  status: 1,
};

const defaultRoute: APIRoute = {
  id: 9,
  api_service_id: 7,
  backend_id: 17,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: ["GET"],
  upstream_path: "/forecast",
  forward_subpath: true,
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
  status: 1,
};

function route(overrides: Partial<APIRoute> = {}): APIRoute {
  return { ...defaultRoute, ...overrides };
}

function tableProps(overrides: Partial<RouteDataTableProps> = {}): RouteDataTableProps {
  return {
    service,
    origin: "https://gateway.test",
    routes: [route()],
    loading: false,
    refreshing: false,
    error: null,
    onRetry: vi.fn(),
    total: 1,
    displayedPage: 1,
    displayedPageSize: 10,
    requestedPage: 1,
    isPlaceholderData: false,
    filters: { search: "" },
    canManage: true,
    onFiltersChange: vi.fn(),
    onExpandedRouteChange: vi.fn(),
    onPaginationChange: vi.fn(),
    onDeleteRoute: vi.fn(),
    renderExpandedRoute: (item) => <div>details-{item.id}</div>,
    ...overrides,
  };
}

function table(overrides: Partial<RouteDataTableProps> = {}) {
  return <RouteDataTable {...tableProps(overrides)} />;
}

describe("RouteDataTable", () => {
  beforeEach(() => {
    hooks.backends.clear();
    hooks.errorIDs.clear();
    hooks.loadingIDs.clear();
    hooks.requestedIDs.length = 0;
    hooks.breakpoint = "lg+";
    hooks.backends.set(17, {
      id: 17,
      api_service_id: 7,
      name: "Primary Target",
      route_count: 1,
      upstream_count: 3,
      enabled_upstream_count: 2,
      endpoint_hosts: ["api.weather.test"],
    });
  });

  it("renders a compact Route identity row and keeps the full URL out of the list", () => {
    render(table());

    const identity = screen.getByText("forecast", { exact: true });
    const row = identity.closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByTitle("/v1/api/weather/forecast/…")).toBeVisible();
    expect(within(row!).getByText("Primary Target")).toBeVisible();
    expect(within(row!).getByText("3 个 Endpoint")).toBeVisible();
    expect(screen.queryByTestId("segmented-url-text")).not.toBeInTheDocument();
    expect(hooks.requestedIDs).toEqual([17]);

    const columns = buildRouteTableColumns({
      service,
      origin: "https://gateway.test",
      canManage: true,
      expandedRouteID: undefined,
      onDeleteRoute: vi.fn(),
      onExpandedRouteChange: vi.fn(),
      t: (key) => messages[key] ?? key,
      compact: false,
    });
    expect(columns.map((column) => column.id)).toEqual([
      "expand",
      "route",
      "methods",
      "target",
      "protocols",
      "status",
      "actions",
    ]);
    expect(columns.map((column) => column.enableHiding)).toEqual([false, false, undefined, undefined, undefined, undefined, false]);
  });

  it("folds secondary columns into Route identity on mobile without a full-URL column", () => {
    hooks.breakpoint = "xs";
    const { container } = render(table());

    const row = screen.getByText("forecast", { exact: true }).closest("tr")!;
    expect(within(row).getByText("GET")).toBeVisible();
    expect(within(row).getByText("启用")).toBeVisible();
    expect(screen.queryByTestId("segmented-url-text")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Route 操作" })).toBeVisible();
    expect(container.querySelector('[data-slot="table-container"]')).toHaveClass("overflow-x-auto");
    expect(container.querySelector('[data-slot="table"]')).toHaveClass("table-fixed");
    expect(screen.getByTestId("route-data-table-region")).toHaveClass(
      "min-w-0",
      "max-w-full",
      "max-sm:[&_[data-slot=select-trigger]]:min-h-11",
      "max-sm:[&_input]:min-h-11",
    );
  });

  it("opens Route portal controls with explicit mobile touch targets", async () => {
    const user = userEvent.setup();
    const { container } = render(table());
    const region = screen.getByTestId("route-data-table-region");

    await user.click(screen.getByRole("combobox", { name: "状态" }));
    for (const option of screen.getAllByRole("option")) {
      expect(region).not.toContainElement(option);
      expect(option).toHaveClass("max-sm:min-h-11");
    }
    expect(document.querySelector('[data-slot="select-content"]')).toHaveClass("max-sm:min-w-44");
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Route 操作" }));
    for (const item of screen.getAllByRole("menuitem")) {
      expect(region).not.toContainElement(item);
      expect(item).toHaveClass("max-sm:min-h-11");
    }
    expect(document.querySelector('[data-slot="dropdown-menu-content"]')).toHaveClass("max-sm:min-w-44");
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "columns" }));
    const columnLabels = screen.getAllByRole("checkbox").map((checkbox) => checkbox.closest("label"));
    expect(columnLabels).toHaveLength(4);
    for (const label of columnLabels) {
      expect(label).not.toBeNull();
      expect(region).not.toContainElement(label);
      expect(label).toHaveClass("max-sm:min-h-11", "max-sm:min-w-44");
    }
    expect(container.querySelector('[data-slot="popover-content"]')).toBeNull();
  });

  it("anchors the expanded Route workspace to the table scroller viewport", () => {
    const { container } = render(table({ expandedRouteID: 9 }));

    const scroller = container.querySelector<HTMLElement>('[data-slot="table-container"]');
    const expandedContent = container.querySelector<HTMLElement>('[data-slot="data-table-expanded-content"]');
    expect(scroller).not.toBeNull();
    expect(expandedContent).not.toBeNull();
    expect(scroller).toHaveAttribute("data-expanded-row-width", "viewport");
    expect(scroller).toContainElement(expandedContent);
    expect(expandedContent).toHaveClass("sticky", "left-0", "w-[100cqw]");
    expect(expandedContent?.closest("td")).toHaveAttribute("colspan", "7");

    Object.defineProperties(scroller!, {
      clientWidth: { configurable: true, value: 349 },
      scrollWidth: { configurable: true, value: 768 },
    });
    scroller!.scrollLeft = 419;
    fireEvent.scroll(scroller!);

    expect(scroller).toHaveProperty("scrollLeft", 419);
    expect(scroller).toContainElement(screen.getByText("details-9"));
  });

  it("uses semantic theme tokens instead of hard-coded palette classes", () => {
    const { container } = render(table());

    expect(container.innerHTML).not.toMatch(/(?:bg|text|border)-(?:gray|slate|zinc|neutral|stone)-/);
  });

  it("keeps the route row visible when its target load fails", () => {
    hooks.errorIDs.add(17);
    render(table());

    expect(screen.getByText("forecast", { exact: true })).toBeVisible();
    expect(screen.getByText("目标加载失败")).toBeVisible();
  });

  it("announces Target loading from the table cell with localized live status", () => {
    hooks.loadingIDs.add(17);
    render(table());

    expect(screen.getByRole("status", { name: "正在加载 Target" })).toHaveAttribute("aria-live", "polite");
  });

  it("compresses methods and preserves all-method semantics at the empty boundary", () => {
    const view = render(table({ routes: [route({ allowed_methods: [] })] }));
    const dataRow = screen.getByText("forecast", { exact: true }).closest("tr");
    expect(within(dataRow!).getByText("全部")).toBeVisible();

    view.rerender(table({ routes: [route({ allowed_methods: ["GET", "POST", "PATCH"] })] }));
    expect(screen.getByText("GET")).toBeVisible();
    expect(screen.getByText("POST")).toBeVisible();
    expect(screen.getByText("+1")).toBeVisible();
    expect(screen.queryByText("PATCH")).not.toBeInTheDocument();
  });

  it("maps row and chevron expansion to one controlled route id", async () => {
    const user = userEvent.setup();
    const onExpandedRouteChange = vi.fn();
    const view = render(table({ onExpandedRouteChange }));

    await user.click(screen.getByRole("button", { name: "展开 forecast" }));
    expect(onExpandedRouteChange).toHaveBeenLastCalledWith(9);

    view.rerender(table({ expandedRouteID: 9, onExpandedRouteChange }));
    expect(screen.getByText("details-9")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "收起 forecast" }));
    expect(onExpandedRouteChange).toHaveBeenLastCalledWith(undefined);
  });

  it("forwards server pagination and keeps toolbar filters controlled", async () => {
    const user = userEvent.setup();
    const onPaginationChange = vi.fn();
    const onFiltersChange = vi.fn();
    render(table({
      total: 21,
      displayedPage: 2,
      requestedPage: 2,
      filters: { search: "forecast", status: 1 },
      refreshing: true,
      onPaginationChange,
      onFiltersChange,
    }));

    expect(screen.getByLabelText("搜索 Route 或完整请求 URL")).toHaveValue("forecast");
    expect(screen.getByRole("combobox", { name: "状态" })).toHaveTextContent("启用");
    expect(screen.getByRole("status", { name: "正在刷新路由" })).toBeVisible();
    expect(screen.getByRole("link", { name: "新建路由" })).toHaveAttribute("href", "/api-services/routes/new?service_id=7");
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(onPaginationChange).toHaveBeenCalledWith(3, 10);

    fireEvent.change(screen.getByLabelText("搜索 Route 或完整请求 URL"), {
      target: { value: " https://gateway.test/v1/api/weather/radar " },
    });
    await vi.waitFor(() => expect(onFiltersChange).toHaveBeenCalledWith({ search: "radar" }));
  });

  it("keeps the displayed page snapshot truthful while the requested page is pending", () => {
    const pageOne = route({ id: 9, slug: "forecast" });
    const pageTwo = route({ id: 10, slug: "radar" });
    const props = {
      total: 21,
      displayedPage: 1,
      requestedPage: 1,
      routes: [pageOne],
    };
    const view = render(table(props));

    expect(screen.getByText("1 / 3")).toBeVisible();
    expect(screen.getByTitle("/v1/api/weather/forecast/…")).toBeVisible();

    view.rerender(table({
      ...props,
      requestedPage: 2,
      isPlaceholderData: true,
      refreshing: true,
    }));

    expect(screen.getByTitle("/v1/api/weather/forecast/…")).toBeVisible();
    expect(screen.getByText("1 / 3")).toBeVisible();
    expect(screen.getByRole("status", { name: "正在刷新路由" })).toBeVisible();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();

    view.rerender(table({
      ...props,
      routes: [pageTwo],
      displayedPage: 2,
      requestedPage: 2,
      isPlaceholderData: false,
      refreshing: false,
    }));

    expect(screen.getByTitle("/v1/api/weather/radar/…")).toBeVisible();
    expect(screen.getByText("2 / 3")).toBeVisible();
  });

  it("keeps a Route query error and retry action inside the table region", async () => {
    const onRetry = vi.fn();
    const user = userEvent.setup();
    render(table({ error: new Error("offline"), onRetry }));

    const region = screen.getByTestId("route-data-table-region");
    expect(within(region).getByRole("alert")).toHaveTextContent("路由加载失败");
    expect(within(region).getByTitle("/v1/api/weather/forecast/…")).toBeVisible();
    await user.click(within(region).getByRole("button", { name: "重试" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("keeps read-only rows free of route mutation actions", async () => {
    render(table({ canManage: false }));
    expect(screen.queryByRole("button", { name: "Route 操作" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "新建路由" })).not.toBeInTheDocument();

    const user = userEvent.setup();
    const onDeleteRoute = vi.fn();
    render(table({ canManage: true, onDeleteRoute }));
    expect(screen.getByRole("link", { name: "新建路由" })).toHaveAttribute(
      "href",
      "/api-services/routes/new?service_id=7",
    );
    await user.click(screen.getAllByRole("button", { name: "Route 操作" }).at(-1)!);
    expect(screen.getByRole("menuitem", { name: "编辑路由" })).toHaveAttribute(
      "href",
      "/api-services/routes/edit?id=9&service_id=7",
    );
    await user.click(screen.getByRole("menuitem", { name: "删除路由" }));
    expect(onDeleteRoute).toHaveBeenCalledWith(expect.objectContaining({ id: 9 }));
  });
});

describe("normalizeRouteTableSearch", () => {
  it("trims ordinary text", () => {
    expect(normalizeRouteTableSearch("  forecast  ", "weather")).toBe("forecast");
  });

  it("extracts and decodes the route segment from a matching public URL", () => {
    expect(normalizeRouteTableSearch(
      "https://gateway.test/v1/api/weather/daily%20report/…?ignored=true",
      "weather",
    )).toBe("daily report");
  });

  it.each([
    "https://gateway.test/v1/api/finance/forecast",
    "https://gateway.test/not-api/weather/forecast",
    "https://gateway.test/v1/api/weather/%E0%A4%A",
  ])("preserves trimmed URL-like input when it is not a valid matching public route: %s", (raw) => {
    expect(normalizeRouteTableSearch(`  ${raw}  `, "weather")).toBe(raw);
  });
});
