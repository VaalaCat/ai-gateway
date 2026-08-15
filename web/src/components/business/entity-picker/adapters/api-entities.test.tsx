import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { beforeEach, describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

const hooks = vi.hoisted(() => ({
  useAPIServices: vi.fn(),
	useAPIService: vi.fn(),
	useAPIBackends: vi.fn(),
	useAPIBackend: vi.fn(),
  useAPIRoutes: vi.fn(),
  useAPIRoute: vi.fn(),
  useAPIUpstreams: vi.fn(),
  useAPIUpstream: vi.fn(),
  useAPIRoles: vi.fn(),
  useAPIRole: vi.fn(),
}));

vi.mock("@/lib/api/api-services", () => hooks);
vi.mock("@/lib/api/api-access", () => hooks);

import { apiServiceAdapter } from "./api-service";
import { apiBackendAdapter } from "./api-backend";
import { apiRouteAdapter } from "./api-route";
import { apiUpstreamAdapter } from "./api-upstream";
import { apiRoleAdapter } from "./api-role";

const query = { data: undefined, isLoading: false, isError: false, refetch: vi.fn() };

describe("Generic API entity adapters", () => {
  beforeEach(() => {
    Object.values(hooks).forEach((hook) => hook.mockReset());
  });

  it.each([
    [1, "enabled", "enabled"],
    [0, "disabled", "disabled"],
  ] as const)("uses runtime locale %s status text for service secondary presentation", (status, enKey, zhKey) => {
    const service = {
      id: 7,
      name: "Weather",
      slug: "weather",
      description: "Forecast",
      price_per_call: 1,
      status,
    };
    expect(apiServiceAdapter.getLabel(service)).toBe("Weather");
    const { rerender } = render(
      <NextIntlClientProvider locale="en" messages={en}>
        {apiServiceAdapter.renderItem?.(service)}
      </NextIntlClientProvider>,
    );
    expect(screen.getByText("weather")).toBeInTheDocument();
    expect(screen.getByText(en.common[enKey])).toBeInTheDocument();

    rerender(
      <NextIntlClientProvider locale="zh" messages={zh}>
        {apiServiceAdapter.renderItem?.(service)}
      </NextIntlClientProvider>,
    );
    expect(screen.getByText("weather")).toBeInTheDocument();
    expect(screen.getByText(zh.common[zhKey])).toBeInTheDocument();
  });

  it("does not load route or upstream candidates until a valid parent service is present", () => {
    hooks.useAPIRoutes.mockReturnValue(query);
    hooks.useAPIUpstreams.mockReturnValue(query);

    apiRouteAdapter.useList({ page_size: 50, enabled: true });
    apiUpstreamAdapter.useList({ page_size: 50, apiServiceId: Number.NaN, enabled: true });

    expect(hooks.useAPIRoutes).toHaveBeenLastCalledWith({ api_service_id: 0, page_size: 50 }, { enabled: false });
    expect(hooks.useAPIUpstreams).toHaveBeenLastCalledWith({ api_service_id: 0, page_size: 50 }, { enabled: false });
  });

	it("queries current service candidates through the management API", () => {
    hooks.useAPIServices.mockReturnValue(query);

    apiServiceAdapter.useList({ search: "weather", page_size: 50, enabled: true });

    expect(hooks.useAPIServices).toHaveBeenLastCalledWith(
      { search: "weather", page_size: 50 },
      { enabled: true },
    );
	});

	it("scopes Backend search to the parent Service and renders Route/Endpoint summary", () => {
		hooks.useAPIBackends.mockReturnValue(query);
		const backend = {
			id: 12,
			api_service_id: 7,
			name: "forecast-primary",
			route_count: 2,
			upstream_count: 3,
			enabled_upstream_count: 1,
			endpoint_hosts: ["api.weather.test"],
		};

		apiBackendAdapter.useList({ apiServiceId: 7, search: "forecast", page_size: 50, enabled: true });
		expect(hooks.useAPIBackends).toHaveBeenLastCalledWith(
			{ api_service_id: 7, search: "forecast", page_size: 50 },
			{ enabled: true },
		);

		render(
			<NextIntlClientProvider locale="en" messages={en}>
				{apiBackendAdapter.renderItem?.(backend)}
			</NextIntlClientProvider>,
		);
		expect(screen.getByText("forecast-primary")).toBeInTheDocument();
		expect(screen.getByText(/2 routes/)).toBeInTheDocument();
		expect(screen.getByText(/api\.weather\.test/)).toBeInTheDocument();
	});

	it("does not load Backend candidates until a valid parent Service is present", () => {
		hooks.useAPIBackends.mockReturnValue(query);
		apiBackendAdapter.useList({ search: "forecast", page_size: 50, enabled: true });

		expect(hooks.useAPIBackends).toHaveBeenLastCalledWith(
			{ api_service_id: 0, search: "forecast", page_size: 50 },
			{ enabled: false },
		);
	});

  it("passes parent service, search and enabled state to route and upstream lists", () => {
    hooks.useAPIRoutes.mockReturnValue(query);
    hooks.useAPIUpstreams.mockReturnValue(query);

    apiRouteAdapter.useList({ apiServiceId: 7, search: "forecast", page_size: 50, enabled: true });
    apiUpstreamAdapter.useList({ apiServiceId: 7, search: "primary", page_size: 50, enabled: false });

    expect(hooks.useAPIRoutes).toHaveBeenLastCalledWith({ api_service_id: 7, search: "forecast", page_size: 50 }, { enabled: true });
    expect(hooks.useAPIUpstreams).toHaveBeenLastCalledWith({ api_service_id: 7, search: "primary", page_size: 50 }, { enabled: false });
  });

  it("searches assignable roles and resolves a selected role through the admin detail endpoint", () => {
    hooks.useAPIRoles.mockReturnValue(query);
    hooks.useAPIRole.mockReturnValue(query);

    apiRoleAdapter.useList({ search: "reader", page_size: 50, enabled: true });
    apiRoleAdapter.useOne("3");

    expect(hooks.useAPIRoles).toHaveBeenLastCalledWith({ search: "reader", page_size: 50, assignable: true }, { enabled: true });
    expect(hooks.useAPIRole).toHaveBeenLastCalledWith(3);
  });

});
