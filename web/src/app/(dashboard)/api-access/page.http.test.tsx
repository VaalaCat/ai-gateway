import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import APIAccessPage from "./page";

const state = vi.hoisted(() => ({ searchParams: new URLSearchParams() }));
const navigation = vi.hoisted(() => ({ replace: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  usePathname: () => "/api-access",
  useRouter: () => navigation,
  useSearchParams: () => state.searchParams,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", () => ({ useCapabilities: () => ({ data: { generic_api: { access: true } }, isLoading: false, isPending: false, error: null }) }));
vi.mock("@/lib/api/api-access", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/api-access")>();
  return { ...actual, useDeleteAPIAccessGrant: () => ({ mutateAsync: vi.fn() }), useReplaceAPIAccessGrant: () => ({ mutateAsync: vi.fn(), isPending: false }) };
});
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("@/components/business/background-refresh-status", () => ({ BackgroundRefreshStatus: () => null }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ data, loading }: { data: Array<{ id?: number; name?: string; principal_id?: number }>; loading: boolean }) => <div data-testid="table">{loading ? "loading" : data.length ? data.map((row) => <span key={row.id ?? row.principal_id}>{row.name ?? `grant-${row.principal_id}`}</span>) : "empty"}</div>,
}));

function renderPage() {
  const queryClient = createTestQueryClient();
  return { queryClient, ...render(<QueryClientProvider client={queryClient}><APIAccessPage /></QueryClientProvider>) };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("APIAccessPage HTTP query isolation", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams();
    navigation.replace.mockReset();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => { vi.unstubAllGlobals(); });

  it("keeps cached role rows visible through roles to grants to roles on one mounted tree", async () => {
	state.searchParams = new URLSearchParams("tab=roles");
    const fetchMock = vi.mocked(fetch).mockImplementation((input) => {
      const path = String(input);
      if (path.startsWith("/api/admin/api-roles")) return Promise.resolve(json({ data: [{ id: 3, key: "reader", name: "Reader", description: "", status: 1, permissions: [] }], total: 1, page: 1, page_size: 20 }));
      return Promise.resolve(json({ data: [{ principal_type: "user", principal_id: 8, api_service_id: 3, configured: { scope: "service", route_ids: [] }, effective: { scope: "service", route_ids: [] }, sources: ["managed"] }], total: 1, page: 1, page_size: 20 }));
    });
    const view = renderPage();
    await screen.findByText("Reader");

    state.searchParams = new URLSearchParams("tab=grants&search=weather&page=2&page_size=50");
    view.rerender(<QueryClientProvider client={view.queryClient}><APIAccessPage /></QueryClientProvider>);
    await screen.findByText("grant-8");
    expect(fetchMock).toHaveBeenCalledWith("/api/admin/api-access-grants?page=2&page_size=50&search=weather", expect.anything());

    state.searchParams = new URLSearchParams("tab=roles");
    view.rerender(<QueryClientProvider client={view.queryClient}><APIAccessPage /></QueryClientProvider>);
    expect(screen.getByText("Reader")).toBeInTheDocument();
  });

  it("sends role and grant URL filters through their independent HTTP requests", async () => {
    const fetchMock = vi.mocked(fetch).mockImplementation(() => Promise.resolve(json({ data: [], total: 0, page: 2, page_size: 50 })));
    state.searchParams = new URLSearchParams("tab=roles&search=weather&status=0&page=2&page_size=50");
    const view = renderPage();
    await screen.findByText("empty");
    expect(fetchMock).toHaveBeenCalledWith("/api/admin/api-roles?page=2&page_size=50&search=weather&status=0", expect.anything());

    state.searchParams = new URLSearchParams("tab=grants&search=weather&page=2&page_size=50");
    view.rerender(<QueryClientProvider client={view.queryClient}><APIAccessPage /></QueryClientProvider>);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/admin/api-access-grants?page=2&page_size=50&search=weather", expect.anything()));
    await waitFor(() => expect(screen.getByTestId("table")).toHaveTextContent("empty"));
  });

  it("keeps a roles 403 local when grants succeeds", async () => {
	state.searchParams = new URLSearchParams("tab=roles");
    vi.mocked(fetch).mockImplementation((input) => {
      const path = String(input);
      if (path.startsWith("/api/admin/api-roles")) return Promise.resolve(json({ error: "forbidden" }, 403));
      return Promise.resolve(json({ data: [{ principal_type: "user", principal_id: 8, api_service_id: 3, configured: { scope: "service", route_ids: [] }, effective: { scope: "service", route_ids: [] }, sources: ["managed"] }], total: 1, page: 1, page_size: 20 }));
    });
    const view = renderPage();
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent("permissionDenied");

    state.searchParams = new URLSearchParams("tab=grants");
    view.rerender(<QueryClientProvider client={view.queryClient}><APIAccessPage /></QueryClientProvider>);
    await screen.findByText("grant-8");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps an ordinary grants failure local when roles succeeds", async () => {
    state.searchParams = new URLSearchParams("tab=grants");
    vi.mocked(fetch).mockImplementation((input) => {
      const path = String(input);
      if (path.startsWith("/api/admin/api-access-grants")) return Promise.resolve(json({ error: "upstream unavailable" }, 500));
      return Promise.resolve(json({ data: [{ id: 3, key: "reader", name: "Reader", description: "", status: 1, permissions: [] }], total: 1, page: 1, page_size: 20 }));
    });
    const view = renderPage();
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent("loadFailed");

    state.searchParams = new URLSearchParams("tab=roles");
    view.rerender(<QueryClientProvider client={view.queryClient}><APIAccessPage /></QueryClientProvider>);
    await screen.findByText("Reader");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
