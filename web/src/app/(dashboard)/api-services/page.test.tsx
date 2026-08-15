import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import APIServicesPage from "./page";

const state = vi.hoisted(() => ({
  searchParams: new URLSearchParams("page=2&page_size=50&search=weather&status=1"),
  capabilityQuery: {
    data: {
      generic_api: {
        services: true,
        access: false,
        logs: false,
        websocket: true,
        service_actions: { create: true, manage_all: true, manage_ids: [] as number[] },
      },
    },
  } as Record<string, unknown>,
  servicesQuery: {
    data: {
      data: [{ id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1, updated_at: 1_720_000_000 }],
      total: 73,
      page: 2,
      page_size: 50,
    },
    error: null,
    isLoading: false,
    isFetching: false,
  } as Record<string, unknown>,
}));

const navigation = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn() }));
const hooks = vi.hoisted(() => ({ list: vi.fn(), update: vi.fn() }));
const table = vi.hoisted(() => ({ props: undefined as Record<string, unknown> | undefined }));
type DataTableMockProps = Record<string, unknown> & { toolbar?: ReactNode | (() => ReactNode); data: Array<{ id: number; name: string }> };

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  usePathname: () => "/api-services",
  useRouter: () => navigation,
  useSearchParams: () => state.searchParams,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/capabilities")>()),
  useCapabilities: () => state.capabilityQuery,
}));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIServices: hooks.list,
  useAPIService: () => ({ data: undefined, error: null, isLoading: false }),
  useAPIRoutes: () => ({ data: { data: [] }, error: null, isLoading: false }),
  useAPIUpstreams: () => ({ data: { data: [] }, error: null, isLoading: false }),
  useCreateAPIService: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAPIService: () => ({ mutateAsync: hooks.update, isPending: false }),
  useDeleteAPIService: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: (props: DataTableMockProps) => {
    table.props = props;
    const toolbar = typeof props.toolbar === "function" ? null : props.toolbar;
    return <div data-testid="data-table">{toolbar}{props.data.map((row) => <span key={row.id}>{row.name}</span>)}</div>;
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: ({ primaryAction, secondaryContent }: { primaryAction?: React.ReactNode; secondaryContent?: React.ReactNode }) => <div>{secondaryContent}{primaryAction}</div>,
}));
vi.mock("@/components/business/background-refresh-status", () => ({
  BackgroundRefreshStatus: ({ refreshing, label }: { refreshing: boolean; label: string }) => refreshing ? <span role="status" aria-label={label} /> : null,
}));

describe("APIServicesPage", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams("page=2&page_size=50&search=weather&status=1");
    state.capabilityQuery = {
      data: {
        generic_api: {
          services: true,
          access: false,
          logs: false,
          websocket: true,
          service_actions: { create: true, manage_all: true, manage_ids: [] },
        },
      },
    };
    state.servicesQuery = {
      data: {
        data: [{ id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1, updated_at: 1_720_000_000 }],
        total: 73,
        page: 2,
        page_size: 50,
      },
      error: null,
      isLoading: false,
      isFetching: false,
    };
    hooks.list.mockReset();
    hooks.list.mockImplementation(() => state.servicesQuery);
    hooks.update.mockReset();
    hooks.update.mockResolvedValue(undefined);
    navigation.replace.mockReset();
    navigation.push.mockReset();
    table.props = undefined;
  });

  it("uses URL filters for the server query and controlled pagination", () => {
    render(<APIServicesPage />);

    expect(hooks.list).toHaveBeenCalledWith({ page: 2, page_size: 50, search: "weather", status: 1 }, { enabled: true });
    expect(table.props).toMatchObject({ total: 73, page: 2, pageSize: 50 });
  });

  it("keeps capability pending distinct from unavailable", () => {
    state.capabilityQuery = { data: undefined, isPending: true };
    const { container, rerender } = render(<APIServicesPage />);
    expect(container.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
    expect(screen.queryByText("unavailable")).not.toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);

    state.capabilityQuery = { data: { generic_api: { services: false } }, isPending: false };
    rerender(<APIServicesPage />);
    expect(screen.getByText("unavailable")).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("does not normalize pagination before the enabled query has response data", async () => {
    state.searchParams = new URLSearchParams("page=3&page_size=20");
    state.capabilityQuery = { data: { generic_api: { services: false } }, isPending: false };
    state.servicesQuery = { data: undefined, error: null, isLoading: false };

    render(<APIServicesPage />);

    await waitFor(() => expect(hooks.list).toHaveBeenCalledWith({ page: 3, page_size: 20 }, { enabled: false }));
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it.each([[403, "permissionDenied"], [500, "loadFailed"]] as const)("maps list error %s to %s", (status, key) => {
    state.servicesQuery = { data: undefined, error: { status }, isLoading: false };
    render(<APIServicesPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(key);
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("keeps rows visible and exposes a background refresh status while fetching", () => {
    state.servicesQuery = { data: { data: [], total: 0, page: 1, page_size: 50 }, error: null, isLoading: false, isFetching: true };
    render(<APIServicesPage />);
    expect(screen.getByTestId("data-table")).toBeInTheDocument();
    expect(table.props).toMatchObject({ data: [], loading: false });
    expect(screen.getByRole("status", { name: "refreshingServices" })).toBeInTheDocument();
  });

  it.each(["NaN", "2", "-1", "1.5"])("does not send invalid status %s and clears it atomically with page", async (status) => {
    state.searchParams = new URLSearchParams(`page=2&status=${status}`);
    render(<APIServicesPage />);
    expect(hooks.list).toHaveBeenCalledWith({ page: 2, page_size: 20 }, { enabled: true });
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-services"));
  });

  it("corrects an empty out-of-range page to the last page", async () => {
    state.searchParams = new URLSearchParams("page=4&page_size=20");
    state.servicesQuery = { data: { data: [], total: 41, page: 4, page_size: 20 }, error: null, isLoading: false };
    render(<APIServicesPage />);
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-services?page=3"));
  });

  it("corrects an empty zero-total page to one and keeps a legal page", async () => {
    state.searchParams = new URLSearchParams("page=4&page_size=20");
    state.servicesQuery = { data: { data: [], total: 0, page: 4, page_size: 20 }, error: null, isLoading: false };
    const view = render(<APIServicesPage />);
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-services"));

    navigation.replace.mockClear();
    state.searchParams = new URLSearchParams("page=2&page_size=20");
    state.servicesQuery = { data: { data: [], total: 21, page: 2, page_size: 20 }, error: null, isLoading: false };
    view.rerender(<APIServicesPage />);
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("hides create and mutation items without explicit action permissions while preserving the detail action column", () => {
    state.capabilityQuery = { data: { generic_api: { services: true, service_actions: { create: false, manage_all: false, manage_ids: [] } } } };
    render(<APIServicesPage />);
    expect(screen.queryByRole("link", { name: "createService" })).not.toBeInTheDocument();
    const columns = table.props?.columns as Array<{ id?: string }>;
    expect(columns.some((column) => column.id === "actions")).toBe(true);
  });

  it("opens the static detail route and static create route", () => {
    render(<APIServicesPage />);
    const columns = table.props?.columns as Array<{ accessorKey?: string; cell?: (context: { row: { original: Record<string, unknown> } }) => React.ReactNode }>;
    const nameCell = columns.find((column) => column.accessorKey === "name")?.cell?.({ row: { original: (state.servicesQuery.data as { data: Record<string, unknown>[] }).data[0] } });
    render(<>{nameCell}</>);
    expect(screen.getByRole("link", { name: "Weather" })).toHaveAttribute("href", "/api-services/detail?id=7");
    expect(screen.getByRole("link", { name: "createService" })).toHaveAttribute("href", "/api-services/new");
  });
});
