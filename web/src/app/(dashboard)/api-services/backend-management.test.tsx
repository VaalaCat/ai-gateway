import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import type { APIBackend, APIRoute } from "@/lib/api/api-services";

import { APIBackendForm } from "./_components/backend-form";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const backend: APIBackend = { id: 17, api_service_id: 7, name: "Forecast production", route_count: 2, upstream_count: 2, enabled_upstream_count: 1, endpoint_hosts: ["a.example.test", "b.example.test"] };
const routes: APIRoute[] = [
  { id: 8, api_service_id: 7, backend_id: 17, slug: "daily", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "/v1", forward_subpath: false, example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" }, status: 1 },
  { id: 9, api_service_id: 7, backend_id: 17, slug: "hourly", protocols: ["http"], allowed_methods: ["GET"], upstream_path: "/v1", forward_subpath: false, example_request: { method: "GET", subpath: "", query: "", headers: {}, body: "" }, status: 1 },
];
const navigation = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
const state = vi.hoisted(() => ({ backend: {} as Record<string, unknown>, routes: {} as Record<string, unknown> }));
const hooks = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string, values?: Record<string, unknown>) => values?.count === undefined ? key : `${key}:${values.count}` }));
vi.mock("next/navigation", () => ({ usePathname: () => "/api-services/detail", useRouter: () => navigation, useSearchParams: () => new URLSearchParams() }));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIBackend: () => state.backend,
  useAllAPIRoutes: () => state.routes,
  useCreateAPIBackend: () => ({ mutateAsync: hooks.create, isPending: false }),
  useUpdateAPIBackend: () => ({ mutateAsync: hooks.update, isPending: false }),
}));

describe("Targets Backend management", () => {
  beforeEach(() => {
    state.backend = { data: backend, isLoading: false, error: null };
    state.routes = { data: routes, isLoading: false, error: null, refetch: vi.fn() };
    hooks.create.mockReset(); hooks.update.mockReset();
    navigation.push.mockReset(); navigation.replace.mockReset();
  });

  it("keeps shared Target impact visible in both the warning and save action", () => {
    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7 }} />);
    expect(screen.getByText("sharedTargetImpact:2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "saveTargetImpact:2" })).toBeInTheDocument();
  });

  it("shows read-only Route and Endpoint impact without a Route picker and returns to the Target hash", async () => {
    hooks.update.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7, returnRoute: { id: 9, slug: "daily-report-9" } }} />);

    expect(screen.getByText("targetImpactSummary", { exact: false })).toHaveTextContent("targetImpactSummary");
    expect(screen.getByText("/daily")).toBeInTheDocument();
    expect(screen.getByText("/hourly")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "routes" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "cancel" }));
    expect(navigation.push).toHaveBeenLastCalledWith("/api-services/detail?id=7&route_search=daily-report-9&route=9");

    await user.click(screen.getByRole("button", { name: "saveTargetImpact:2" }));
    expect(navigation.push).toHaveBeenLastCalledWith("/api-services/detail?id=7&route_search=daily-report-9&route=9");
  });

  it("keeps aggregate impact visible while the referencing Route list is loading", () => {
    state.routes = { data: undefined, isLoading: true, error: null, refetch: vi.fn() };

    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7 }} />);

    expect(screen.getByText("targetImpactSummary", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("status", { name: "targetRoutesLoading" })).toBeInTheDocument();
    expect(screen.queryByText("targetNoRoutes")).not.toBeInTheDocument();
  });

  it("keeps Target editing available when the referencing Route list fails and retries locally", async () => {
    const refetch = vi.fn();
    state.routes = { data: undefined, isLoading: false, error: new Error("route list rejected"), refetch };
    hooks.update.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();

    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7 }} />);

    expect(screen.getByText("targetRoutesLoadFailed")).toBeInTheDocument();
    expect(screen.queryByText("targetNoRoutes")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "saveTargetImpact:2" }));
    expect(hooks.update).toHaveBeenCalledWith({ id: 17, name: "Forecast production" });
    expect(navigation.push).toHaveBeenLastCalledWith("/api-services/detail?id=7");
  });

  it("shows no referencing Routes only after a successful empty Route query", () => {
    state.routes = { data: [], isLoading: false, error: null, refetch: vi.fn() };

    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7 }} />);

    expect(screen.getByText("targetNoRoutes")).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "targetRoutesLoading" })).not.toBeInTheDocument();
    expect(screen.getByTestId("target-route-list").querySelector('[role="alert"]')).not.toBeInTheDocument();
  });

  it("returns a newly created Target to the response ID hash", async () => {
    hooks.create.mockResolvedValue({ ...backend, id: 29 });
    const user = userEvent.setup();
    render(<APIBackendForm mode={{ kind: "create", serviceId: 7 }} />);

    await user.type(screen.getByLabelText("name"), "New target");
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=7");
  });

  it("keeps an edited Target name empty instead of silently restoring the saved value", async () => {
    const user = userEvent.setup();
    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7 }} />);

    await user.clear(screen.getByLabelText("name"));
    expect(screen.getByLabelText("name")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "saveTargetImpact:2" }));
    expect(hooks.update).not.toHaveBeenCalled();
  });

  it("keeps a contextual Target edit in place when its mutation fails", async () => {
    hooks.update.mockRejectedValue(new Error("target update rejected"));
    const user = userEvent.setup();
    render(<APIBackendForm mode={{ kind: "edit", id: 17, serviceId: 7, returnRoute: { id: 9, slug: "forecast-v2" } }} />);

    await user.click(screen.getByRole("button", { name: "saveTargetImpact:2" }));
    expect(await screen.findByText("target update rejected")).toBeVisible();
    expect(navigation.push).not.toHaveBeenCalled();
  });

});
