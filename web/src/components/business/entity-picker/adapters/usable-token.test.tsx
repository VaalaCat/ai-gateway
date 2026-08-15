import { fireEvent, render, renderHook, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { buildQuery } from "@/lib/api/client";
import type { useTokens } from "@/lib/api/tokens";
import { EntityPicker } from "../entity-picker";
import { usableTokenAdapter } from "./usable-token";

const usableOnlyTokenParams: NonNullable<Parameters<typeof useTokens>[0]> = { usable_only: true };

const state = vi.hoisted(() => ({
  userId: 7,
	isAdmin: false,
  params: vi.fn(),
  options: vi.fn(),
  useToken: vi.fn(),
  useUsableTokenForAPIRoute: vi.fn(),
	listItems: [] as Array<{ id: number; user_id: number; name: string; owner_username: string }>,
	selectedItems: [] as Array<{ id: number; user_id: number; name: string; owner_username: string }>,
	selectedError: false,
}));

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ user: { user_id: state.userId }, isAdmin: state.isAdmin }),
}));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/components/business/admin-scope-toggle", () => ({
	AdminScopeToggle: ({ onChange }: { onChange: (value: "self" | "global") => void }) => (
		<button type="button" onClick={() => onChange("self")}>scope-self</button>
	),
}));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: (params: unknown, options: unknown) => {
    state.params(params);
    state.options(options);
		const isSelectedValueQuery = typeof params === "object" && params !== null && "token_id" in params;
		return {
			data: { data: isSelectedValueQuery ? state.selectedItems : state.listItems },
			isLoading: false,
			isError: isSelectedValueQuery && state.selectedError,
			error: isSelectedValueQuery && state.selectedError ? new Error("selected Token lookup failed") : null,
			refetch: vi.fn(),
		};
  },
  useToken: (id: number, options: unknown) => {
    state.useToken(id, options);
    return { data: undefined };
  },
  useUsableTokenForAPIRoute: (scope: unknown) => {
    state.useUsableTokenForAPIRoute(scope);
    return { data: undefined };
  },
}));

beforeEach(() => {
	HTMLElement.prototype.scrollIntoView = vi.fn();
	state.isAdmin = false;
	state.params.mockReset();
  state.options.mockReset();
  state.useToken.mockReset();
  state.useUsableTokenForAPIRoute.mockReset();
	state.listItems = [];
	state.selectedItems = [];
	state.selectedError = false;
});

function listRequestParams() {
	return state.params.mock.calls.map(([params]) => params).reverse().find((params) => (
		typeof params === "object"
		&& params !== null
		&& "page_size" in params
		&& !("token_id" in params)
	));
}

it("starts an admin catalog picker in all scope when defaultAdminScope is all", () => {
	state.isAdmin = true;
	render(<EntityPicker entity="usable-token" value="" onChange={() => {}} defaultAdminScope="all" />);

	expect(listRequestParams()).toEqual({ search: "", page_size: 50, usable_only: true });
});

it("uses the current user after an admin catalog picker switches to self scope", () => {
	state.isAdmin = true;
	render(<EntityPicker entity="usable-token" value="" onChange={() => {}} defaultAdminScope="all" />);

	fireEvent.click(screen.getByRole("combobox"));
	fireEvent.click(screen.getByRole("button", { name: "scope-self" }));

	expect(listRequestParams()).toEqual({
		search: "",
		page_size: 50,
		user_id: 7,
		usable_only: true,
	});
});

it("normalizes a non-admin catalog picker to self even when all is requested", () => {
	render(<EntityPicker entity="usable-token" value="" onChange={() => {}} defaultAdminScope="all" />);

	expect(listRequestParams()).toEqual({
		search: "",
		page_size: 50,
		user_id: 7,
		usable_only: true,
	});
});

it("keeps an admin picker without defaultAdminScope in self scope", () => {
	state.isAdmin = true;
	render(<EntityPicker entity="usable-token" value="" onChange={() => {}} />);

	expect(listRequestParams()).toEqual({
		search: "",
		page_size: 50,
		user_id: 7,
		usable_only: true,
	});
});

it("shows a Token name and owner username in list candidates and the selected value", () => {
	state.isAdmin = true;
	state.listItems = [{ id: 31, user_id: 99, name: "catalog-token", owner_username: "alice" }];
	state.selectedItems = [{ id: 31, user_id: 99, name: "catalog-token", owner_username: "alice" }];
	render(<EntityPicker entity="usable-token" value="31" onChange={() => {}} defaultAdminScope="all" />);

	expect(screen.getByRole("combobox")).toHaveTextContent("catalog-token");
	expect(screen.getByRole("combobox")).toHaveTextContent("alice");
	expect(screen.getByRole("combobox")).not.toHaveTextContent("99");
	fireEvent.click(screen.getByRole("combobox"));
	expect(screen.getByRole("option", { name: /catalog-token.*alice/ })).toBeInTheDocument();
	expect(screen.getByRole("option", { name: /catalog-token.*alice/ })).not.toHaveTextContent("99");
});

it("uses the list Token DTO for an unscoped selected-value lookup and shows its error state", () => {
	state.selectedError = true;
	render(<EntityPicker entity="usable-token" value="31" onChange={() => {}} placeholder="choose a Token" />);

	expect(state.params).toHaveBeenCalledWith(expect.objectContaining({
		token_id: 31,
		usable_only: true,
		user_id: 7,
	}));
	expect(screen.getByRole("combobox")).not.toHaveTextContent("31");
	fireEvent.click(screen.getByRole("combobox"));
	expect(document.querySelector('[data-slot="entity-picker-selected-error"]')).toHaveAttribute("data-state", "error");
	expect(document.querySelector('[data-slot="entity-picker-selected-retry"]')).toHaveAttribute("data-state", "error");
});

it("hydrates an unscoped selected Token when the exact ID is returned", () => {
	state.selectedItems = [{ id: 31, user_id: 7, name: "selected-token", owner_username: "alice" }];
	const { result } = renderHook(() => usableTokenAdapter.useOne("31", { scope: "self" }));

	expect(result.current.data).toMatchObject({ id: 31, user_id: 7, name: "selected-token" });
});

it("skips an unrelated first Token when hydrating an unscoped selected value", () => {
	state.selectedItems = [
		{ id: 99, user_id: 7, name: "wrong-first", owner_username: "alice" },
		{ id: 31, user_id: 7, name: "selected-token", owner_username: "alice" },
	];
	const { result } = renderHook(() => usableTokenAdapter.useOne("31", { scope: "self" }));

	expect(result.current.data).toMatchObject({ id: 31, name: "selected-token" });
});

it("does not hydrate a selected Token from another owner", () => {
	state.selectedItems = [{ id: 31, user_id: 42, name: "foreign-token", owner_username: "bob" }];
	const { result } = renderHook(() => usableTokenAdapter.useOne("31", { scope: "self" }));

	expect(result.current.data).toBeUndefined();
});

it("serializes usable_only in the Token query", () => {
  expect(buildQuery(usableOnlyTokenParams)).toBe("?usable_only=true");
});

it("requests only usable Tokens for the current user", () => {
  renderHook(() => usableTokenAdapter.useList({
    search: "prod",
    scope: "self",
    page_size: 50,
  }));

  expect(state.params).toHaveBeenCalledWith({
    search: "prod",
    page_size: 50,
    user_id: 7,
    usable_only: true,
  });
  expect(state.options).toHaveBeenCalledWith({
    enabled: true,
    cacheScope: ["viewer", 7, "api-route", 0, 0],
  });
});

it("disables the Token list query while its picker is closed", () => {
  renderHook(() => usableTokenAdapter.useList({
    search: "",
    scope: "self",
    page_size: 50,
    enabled: false,
  }));

  expect(state.options).toHaveBeenCalledWith({
    enabled: false,
    cacheScope: ["viewer", 7, "api-route", 0, 0],
  });
});

it("keeps admin all scope while retaining usable_only", () => {
  renderHook(() => usableTokenAdapter.useList({ search: "", scope: "all", page_size: 50 }));

  expect(state.params.mock.calls[0][0]).toEqual({ search: "", page_size: 50, usable_only: true });
  expect(state.options).toHaveBeenCalledWith({
    enabled: true,
    cacheScope: ["viewer", 7, "api-route", 0, 0],
  });
});

it("prefers an explicit owner", () => {
  renderHook(() => usableTokenAdapter.useList({
    search: "",
    scope: "all",
    page_size: 50,
    ownerUserId: 42,
  }));

  expect(state.params).toHaveBeenCalledWith({
    search: "",
    page_size: 50,
    user_id: 42,
    usable_only: true,
  });
});

it("passes the Service and Route scope required for invoke filtering", () => {
	renderHook(() => usableTokenAdapter.useList({
		search: "forecast",
		scope: "all",
		page_size: 50,
		apiServiceId: 7,
		apiRouteId: 9,
	}));

	expect(state.params).toHaveBeenCalledWith({
		search: "forecast",
		page_size: 50,
		usable_only: true,
		api_service_id: 7,
		api_route_id: 9,
	});
  expect(state.options).toHaveBeenCalledWith({
    enabled: true,
    cacheScope: ["viewer", 7, "api-route", 7, 9],
  });
});

it("fails closed when only one API invoke scope ID is present", () => {
	renderHook(() => usableTokenAdapter.useList({
		search: "",
		scope: "all",
		page_size: 50,
		apiServiceId: 7,
	}));

	expect(state.params).toHaveBeenCalledWith({
		search: "",
		page_size: 50,
		usable_only: true,
		api_service_id: 7,
		api_route_id: 0,
	});
  expect(state.options).toHaveBeenCalledWith({
    enabled: false,
    cacheScope: ["viewer", 7, "api-route", 7, 0],
  });
});

it("hydrates a selected API-scoped value only through the scoped usable-token query", () => {
  renderHook(() => usableTokenAdapter.useOne("5", {
    scope: "all",
    apiServiceId: 7,
    apiRouteId: 9,
  }));

  expect(state.useUsableTokenForAPIRoute).toHaveBeenCalledWith({
    viewerUserID: 7,
    apiServiceID: 7,
    apiRouteID: 9,
    tokenID: 5,
  });
  expect(state.useToken).not.toHaveBeenCalledWith(5, expect.objectContaining({ enabled: true }));
});

it("passes an explicit owner through selected API-scoped validation", () => {
  renderHook(() => usableTokenAdapter.useOne("5", {
    scope: "all",
    ownerUserId: 42,
    apiServiceId: 7,
    apiRouteId: 9,
  }));

  expect(state.useUsableTokenForAPIRoute).toHaveBeenCalledWith({
    viewerUserID: 7,
    ownerUserID: 42,
    apiServiceID: 7,
    apiRouteID: 9,
    tokenID: 5,
  });
});
