import { useSyncExternalStore } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRoute, APIService } from "@/lib/api/api-services";
import { createTestQueryClient } from "@/test/render";
import { APIServiceWorkspace } from "./page";

const state = vi.hoisted(() => ({ revision: 0, params: new URLSearchParams("id=7&route=9") }));
const navigation = vi.hoisted(() => ({ replace: vi.fn() }));
const apiGet = vi.hoisted(() => vi.fn());

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  useRouter: () => navigation,
  useSearchParams: () => {
    const snapshot = useSyncExternalStore((notify) => {
      const update = () => { state.params = new URLSearchParams(window.location.search); state.revision += 1; notify(); };
      window.addEventListener("popstate", update);
      return () => window.removeEventListener("popstate", update);
    }, () => `${state.revision}:${state.params}`, () => "0:");
    return new URLSearchParams(snapshot.slice(snapshot.indexOf(":") + 1));
  },
}));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { ...actual.api, get: apiGet } };
});
vi.mock("@/lib/api/api-services", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/api-services")>();
  return {
    ...actual,
    useDeleteAPIService: () => ({ mutateAsync: vi.fn() }),
    useDeleteAPIRoute: () => ({ mutateAsync: vi.fn() }),
    useDeleteAPIBackend: () => ({ mutateAsync: vi.fn() }),
    useDeleteAPIUpstream: () => ({ mutateAsync: vi.fn() }),
  };
});
vi.mock("../_components/route-table/route-data-table", () => ({
  RouteDataTable: ({ routes, expandedRouteID, renderExpandedRoute }: {
    routes: APIRoute[];
    expandedRouteID?: number;
    renderExpandedRoute: (route: APIRoute) => React.ReactNode;
  }) => <div>{routes.map((route) => <div key={route.id}>row:{route.slug}{expandedRouteID === route.id ? renderExpandedRoute(route) : null}</div>)}</div>,
}));
vi.mock("../_components/route-table/route-expanded-workspace", () => ({
  RouteExpandedWorkspace: ({ route }: { route: APIRoute }) => <div>workspace:{route.slug}</div>,
}));

const service: APIService = { id: 7, slug: "weather", name: "Weather", description: "", price_per_call: 1, status: 1 };
const route = (slug: string): APIRoute => ({
  id: 9, api_service_id: 7, backend_id: 17, slug, protocols: ["http"], allowed_methods: ["GET"],
  upstream_path: slug, forward_subpath: false,
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" }, status: 1,
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((success) => { resolve = success; });
  return { promise, resolve };
}

describe("Route page placeholder transition", () => {
  beforeEach(() => {
    state.revision = 0;
    state.params = new URLSearchParams("id=7&route=9");
    window.history.replaceState({}, "", "/api-services/detail?id=7&route=9");
    navigation.replace.mockReset();
    apiGet.mockReset();
  });

  it("keeps previous rows but unmounts their workspace until the requested page resolves", async () => {
    const secondPage = deferred<{ data: APIRoute[]; total: number; page: number; page_size: number }>();
    apiGet
      .mockResolvedValueOnce({ data: [route("page-one")], total: 2, page: 1, page_size: 10 })
      .mockReturnValueOnce(secondPage.promise);
    render(
      <QueryClientProvider client={createTestQueryClient()}>
        <APIServiceWorkspace service={service} canManage origin="https://gateway.test" />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("workspace:page-one")).toBeVisible();

    act(() => {
      window.history.replaceState({}, "", "/api-services/detail?id=7&route_page=2&route=9");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(apiGet).toHaveBeenLastCalledWith("/admin/api-routes?api_service_id=7&page=2&page_size=10"));
    expect(screen.getByText("row:page-one")).toBeVisible();
    expect(screen.queryByText("workspace:page-one")).not.toBeInTheDocument();

    secondPage.resolve({ data: [route("page-two")], total: 2, page: 2, page_size: 10 });
    expect(await screen.findByText("workspace:page-two")).toBeVisible();
    expect(screen.queryByText("row:page-one")).not.toBeInTheDocument();
  });
});
