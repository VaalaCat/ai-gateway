import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import EditAPIServicePage from "./edit/page";
import NewAPIServicePage from "./new/page";
import EditRoutePage from "./routes/edit/page";
import NewRoutePage from "./routes/new/page";
import EditBackendPage from "./backends/edit/page";
import NewBackendPage from "./backends/new/page";
import EditUpstreamPage from "./upstreams/edit/page";
import NewUpstreamPage from "./upstreams/new/page";

const state = vi.hoisted(() => ({
  searchParams: new URLSearchParams(),
  capability: { data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] as number[] } } } } as Record<string, unknown>,
  service: { data: { id: 7, slug: "weather", name: "Weather" }, isLoading: false, error: null, refetch: vi.fn() } as Record<string, unknown>,
}));
const routeForm = vi.hoisted(() => vi.fn());
const upstreamForm = vi.hoisted(() => vi.fn());
const backendForm = vi.hoisted(() => vi.fn());

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useSearchParams: () => state.searchParams }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/capabilities")>()),
  useCapabilities: () => state.capability,
}));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIService: () => state.service,
}));
vi.mock("./_components/service-form", () => ({ APIServiceForm: () => <div>service-form-mounted</div> }));
vi.mock("./_components/route-form", () => ({ APIRouteForm: (props: unknown) => { routeForm(props); return <div>route-form-mounted</div>; } }));
vi.mock("./_components/upstream-form", () => ({ APIUpstreamForm: (props: unknown) => { upstreamForm(props); return <div>upstream-form-mounted</div>; } }));
vi.mock("./_components/backend-form", () => ({ APIBackendForm: (props: unknown) => { backendForm(props); return <div>backend-form-mounted</div>; } }));

describe("API service static form entries", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams();
    state.capability = { data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] } } } };
    state.service = { data: { id: 7, slug: "weather", name: "Weather" }, isLoading: false, error: null, refetch: vi.fn() };
    routeForm.mockReset();
    upstreamForm.mockReset();
    backendForm.mockReset();
  });

  it.each([
    ["service edit", EditAPIServicePage, "id=0", "serviceNotFound"],
    ["route create", NewRoutePage, "service_id=NaN", "serviceNotFound"],
    ["route edit id", EditRoutePage, "id=-1&service_id=7", "routeNotFound"],
    ["route edit parent", EditRoutePage, "id=9&service_id=2.5", "routeNotFound"],
    ["upstream create", NewUpstreamPage, "service_id=", "serviceNotFound"],
    ["upstream edit id", EditUpstreamPage, "id=abc&service_id=7", "upstreamNotFound"],
    ["upstream edit parent", EditUpstreamPage, "id=3&service_id=0", "upstreamNotFound"],
  ])("rejects an invalid %s identity before mounting a form", (_name, Page, params, expected) => {
    state.searchParams = new URLSearchParams(params);
    render(<Page />);
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.queryByText(/form-mounted/)).not.toBeInTheDocument();
  });

  it.each([
    [NewBackendPage, "service_id=7&route_id=9&route_slug=daily-report-9", "create"],
    [EditBackendPage, "id=17&service_id=7&route_id=9&route_slug=daily-report-9", "edit"],
  ] as const)("passes Route context through Target %s entry", (Page, params, kind) => {
    state.searchParams = new URLSearchParams(params);
    render(<Page />);
    expect(backendForm).toHaveBeenCalledWith(expect.objectContaining({ mode: expect.objectContaining({ kind, returnRoute: { id: 9, slug: "daily-report-9" } }) }));
  });

  it.each([
    "service_id=7&route_id=9",
    "service_id=7&route_slug=forecast-v2",
    "service_id=7&route_id=9&route_slug=Forecast",
    "service_id=7&route_id=9&route_slug=forecast%2Fv2",
    "service_id=7&route_id=9&route_slug=%E6%9D%B1%E4%BA%AC",
    `service_id=7&route_id=9&route_slug=${"a".repeat(65)}`,
  ])("drops incomplete or invalid Route context at a Target entry: %s", (params) => {
    state.searchParams = new URLSearchParams(params);
    render(<NewBackendPage />);
    expect(backendForm).toHaveBeenCalledWith(expect.objectContaining({ mode: expect.not.objectContaining({ returnRoute: expect.anything() }) }));
  });

  it.each([
    ["create", NewRoutePage, "service_id=7"],
    ["edit", EditRoutePage, "id=9&service_id=7"],
  ])("offers retry for a non-404 service load failure on %s", async (_name, Page, params) => {
    state.searchParams = new URLSearchParams(params);
    const refetch = vi.fn();
    state.service = { data: undefined, isLoading: false, error: { status: 500 }, refetch };
    render(<Page />);

    expect(screen.getByText("loadFailed")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("does not mount a direct create form while capability is pending", () => {
    state.capability = { data: undefined, isPending: true };
    const { container } = render(<NewAPIServicePage />);
    expect(container.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
    expect(screen.queryByText("service-form-mounted")).not.toBeInTheDocument();
  });

  it.each([
    ["service edit", EditAPIServicePage, "id=7", "service-form-mounted"],
    ["route create", NewRoutePage, "service_id=7", "route-form-mounted"],
    ["route edit", EditRoutePage, "id=9&service_id=7", "route-form-mounted"],
    ["upstream create", NewUpstreamPage, "service_id=7", "upstream-form-mounted"],
    ["upstream edit", EditUpstreamPage, "id=3&service_id=7", "upstream-form-mounted"],
  ])("fails closed for a direct %s without manage permission", (_name, Page, params, formText) => {
    state.searchParams = new URLSearchParams(params);
    state.capability = { data: { generic_api: { services: true, service_actions: { create: true, manage_all: false, manage_ids: [] } } } };
    render(<Page />);
    expect(screen.getByText("permissionDenied")).toBeInTheDocument();
    expect(screen.queryByText(formText)).not.toBeInTheDocument();
  });

  it("distinguishes unavailable, forbidden, and ordinary capability failures", () => {
    state.capability = { data: { generic_api: { services: false } } };
    const { rerender } = render(<NewAPIServicePage />);
    expect(screen.getByText("unavailable")).toBeInTheDocument();

    state.capability = { data: undefined, error: { status: 403 } };
    rerender(<NewAPIServicePage />);
    expect(screen.getByText("permissionDenied")).toBeInTheDocument();

    state.capability = { data: undefined, error: { status: 503 } };
    rerender(<NewAPIServicePage />);
    expect(screen.getByText("loadFailed")).toBeInTheDocument();
  });

  it("allows create only with explicit create permission", () => {
    state.capability = { data: { generic_api: { services: true, service_actions: { create: false, manage_all: true, manage_ids: [] } } } };
    render(<NewAPIServicePage />);
    expect(screen.getByText("permissionDenied")).toBeInTheDocument();
    expect(screen.queryByText("service-form-mounted")).not.toBeInTheDocument();
  });

  it.each([
    ["create", NewRoutePage, "service_id=7"],
    ["edit", EditRoutePage, "id=9&service_id=7"],
  ])("loads the real service slug before mounting the %s route form", (_name, Page, params) => {
    state.searchParams = new URLSearchParams(params);
    render(<Page />);

    expect(routeForm).toHaveBeenCalledWith(expect.objectContaining({ mode: expect.objectContaining({ serviceId: 7, serviceSlug: "weather" }) }));
  });

  it.each([
    [NewUpstreamPage, "service_id=7&backend_id=12&route_id=9&route_slug=daily-report-9", "create"],
    [EditUpstreamPage, "id=3&service_id=7&route_id=9&route_slug=daily-report-9", "edit"],
    [NewUpstreamPage, "service_id=7&copy_id=3&route_id=9&route_slug=daily-report-9", "copy"],
  ] as const)("passes Route context through Endpoint %s entry", (Page, params, kind) => {
    state.searchParams = new URLSearchParams(params);
    render(<Page />);
    expect(upstreamForm).toHaveBeenCalledWith(expect.objectContaining({ mode: expect.objectContaining({ kind, returnRoute: { id: 9, slug: "daily-report-9" } }) }));
  });

  it.each([
    "service_id=7&backend_id=12&route_id=9",
    "service_id=7&backend_id=12&route_slug=forecast",
    "service_id=7&backend_id=12&route_id=0&route_slug=forecast",
    "service_id=7&backend_id=12&route_id=9&route_slug=Forecast",
    "service_id=7&backend_id=12&route_id=9&route_slug=forecast%2Fv2",
    "service_id=7&backend_id=12&route_id=9&route_slug=daily%20report",
    "service_id=7&backend_id=12&route_id=9&route_slug=%E6%9D%B1%E4%BA%AC",
    `service_id=7&backend_id=12&route_id=9&route_slug=${"a".repeat(65)}`,
  ])("drops incomplete or invalid Route context at an Endpoint entry: %s", (params) => {
    state.searchParams = new URLSearchParams(params);
    render(<NewUpstreamPage />);
    expect(upstreamForm).toHaveBeenCalledWith(expect.objectContaining({ mode: expect.not.objectContaining({ returnRoute: expect.anything() }) }));
  });
});
