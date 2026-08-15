import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { BindingsTable } from "./_components/bindings-table";
import { RolesTable } from "./_components/roles-table";
import type { APIRole, APIRoleBinding } from "@/lib/api/api-access";

const state = vi.hoisted(() => ({ searchParams: new URLSearchParams("page=2"), roles: { data: { data: [] as APIRole[], total: 0, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null }, bindings: { data: { data: [] as APIRoleBinding[], total: 0, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null } }));
const navigation = vi.hoisted(() => ({ replace: vi.fn() }));
const hooks = vi.hoisted(() => ({ roles: vi.fn(), bindings: vi.fn() }));
const mutations = vi.hoisted(() => ({ deleteRole: vi.fn(), deleteBinding: vi.fn() }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ usePathname: () => "/api-access", useRouter: () => navigation, useSearchParams: () => state.searchParams }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 }, isAdmin: false }) }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data }: { columns: Array<{ id?: string; cell?: (context: { row: { original: { id: number } } }) => React.ReactNode }>; data: Array<{ id: number }> }) => {
    const actions = columns.find((column) => column.id === "actions");
    return <div data-testid="table">{data.map((row) => <div key={row.id}>{actions?.cell?.({ row: { original: row } })}</div>)}</div>;
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("@/components/business/background-refresh-status", () => ({ BackgroundRefreshStatus: () => null }));
vi.mock("@/lib/api/api-access", () => ({
  useAPIRoles: hooks.roles,
  useAPIRoleBindings: hooks.bindings,
  useDeleteAPIRole: () => ({ mutateAsync: mutations.deleteRole }),
  useDeleteAPIRoleBinding: () => ({ mutateAsync: mutations.deleteBinding }),
}));

describe("API access table pagination", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams("page=2");
    state.roles = { data: { data: [], total: 0, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null };
    state.bindings = { data: { data: [], total: 0, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null };
    hooks.roles.mockReset(); hooks.roles.mockImplementation(() => state.roles);
    hooks.bindings.mockReset(); hooks.bindings.mockImplementation(() => state.bindings);
    mutations.deleteRole.mockReset(); mutations.deleteRole.mockResolvedValue(undefined);
    mutations.deleteBinding.mockReset(); mutations.deleteBinding.mockResolvedValue(undefined);
    navigation.replace.mockReset();
  });

  it("corrects an empty roles page to one even when the total becomes zero", async () => {
    render(<RolesTable enabled />);
    expect(hooks.roles).toHaveBeenCalledWith({ page: 2, page_size: 20 }, { enabled: true });
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-access"));
  });

  it("corrects an empty bindings page to one even when the total becomes zero", async () => {
    render(<BindingsTable enabled />);
    expect(hooks.bindings).toHaveBeenCalledWith({ page: 2, page_size: 20 }, { enabled: true });
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-access"));
  });

  it("corrects the roles URL after deleting the last row refetches an empty page", async () => {
    const user = userEvent.setup();
    state.roles = { data: { data: [{ id: 7, key: "reader", name: "Reader", description: "", status: 1, permissions: [], members: [] }], total: 21, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null };
    mutations.deleteRole.mockImplementation(async () => {
      state.roles = { data: { data: [], total: 0, page: 2, page_size: 20 }, isLoading: false, isFetching: false, error: null };
    });
    render(<RolesTable enabled />);
    await user.click(screen.getByRole("button", { name: "deleteRole" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    await waitFor(() => expect(mutations.deleteRole).toHaveBeenCalledWith(7));
    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-access"));
  });

  it("hides role actions for gateway_admin but keeps them for a custom role", () => {
    state.searchParams = new URLSearchParams();
    state.roles = { data: { data: [{ id: 5, key: "gateway_admin", name: "Gateway admin", description: "", built_in: false, status: 1, permissions: [], members: [] }, { id: 6, key: "reader", name: "Reader", description: "", built_in: false, status: 1, permissions: [], members: [] }], total: 2, page: 1, page_size: 20 }, isLoading: false, isFetching: false, error: null };
    render(<RolesTable enabled />);
    expect(screen.getAllByRole("button", { name: "deleteRole" })).toHaveLength(1);
  });
});
