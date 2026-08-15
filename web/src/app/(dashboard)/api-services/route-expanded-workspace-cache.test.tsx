import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute, APIService, APIUpstream } from "@/lib/api/api-services";
import { createTestQueryClient } from "@/test/render";

import { RouteExpandedWorkspace } from "./_components/route-table/route-expanded-workspace";

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { ...actual.api, get: requests.get, post: requests.post } };
});

const service: APIService = { id: 7, slug: "weather", name: "Weather", description: "", price_per_call: 1, status: 1 };
const route: APIRoute = {
  id: 9, api_service_id: 7, backend_id: 17, slug: "forecast", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "/forecast", forward_subpath: true,
  example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" }, status: 1, updated_at: 100,
};
const target: APIBackend = { id: 17, api_service_id: 7, name: "Primary", route_count: 1, upstream_count: 2, enabled_upstream_count: 2, endpoint_hosts: ["a.test", "b.test"], updated_at: 200 };

function endpoint(id: number, baseURL: string): APIUpstream {
  return { id, backend_id: 17, name: `Endpoint ${id}`, base_url: baseURL, weight: 1, priority: id, auth_type: "none", status: 1, credential_configured: false, proxy_url_configured: false };
}

function page(data: APIUpstream[]) {
  return { data, total: data.length, page: 1, page_size: 100 };
}

describe("RouteExpandedWorkspace configured Preview cache", () => {
  beforeEach(() => {
    requests.get.mockReset();
    requests.post.mockReset();
  });

  it("reuses equivalent Endpoint dependencies and refetches a changed Endpoint inside the fresh window", async () => {
    const initialEndpoints = [endpoint(31, "https://a.test"), endpoint(32, "https://b.test")];
    requests.get.mockImplementation((path: string) => {
      if (path === "/admin/api-backends/17") return Promise.resolve(target);
      if (path.startsWith("/admin/api-upstreams?")) return Promise.resolve(page(initialEndpoints));
      return Promise.reject(new Error(`Unexpected GET ${path}`));
    });
    requests.post
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 31, upstream_name: "Endpoint 31", status: 1, priority: 31, weight: 1, final_url: "https://a.test/forecast" }], diagnostics: [] })
      .mockResolvedValueOnce({ endpoints: [{ upstream_id: 31, upstream_name: "Endpoint 31", status: 1, priority: 31, weight: 1, final_url: "https://new-a.test/forecast" }], diagnostics: [] });
    const client = createTestQueryClient();
    render(
      <QueryClientProvider client={client}>
        <RouteExpandedWorkspace service={service} origin="https://gateway.test" route={route} canManage onDeleteTarget={vi.fn()} onDeleteEndpoint={vi.fn()} />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(requests.post).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://a.test/forecast/…")).toBe(true));

    await act(async () => {
      client.setQueryData(["api-upstreams", "all", 17], [...initialEndpoints].reverse().map((item) => ({ ...item })));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(requests.post).toHaveBeenCalledTimes(1);

    await act(async () => {
      client.setQueryData(["api-upstreams", "all", 17], [endpoint(31, "https://new-a.test"), initialEndpoints[1]]);
    });
    await waitFor(() => expect(requests.post).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getAllByTestId("segmented-url-text").some((item) => item.textContent === "https://new-a.test/forecast/…")).toBe(true));
  });
});
