import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import APIAccessPage from "./page";

const state = vi.hoisted(() => ({
  searchParams: new URLSearchParams(),
  capability: { data: { generic_api: { access: true } }, error: null, isLoading: false, isPending: false } as Record<string, unknown>,
}));
const navigation = vi.hoisted(() => ({ replace: vi.fn() }));
const hooks = vi.hoisted(() => ({ roles: vi.fn(), grants: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  usePathname: () => "/api-access",
  useRouter: () => navigation,
  useSearchParams: () => state.searchParams,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", () => ({ useCapabilities: () => state.capability }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("@/lib/api/api-access", () => ({
  useAPIRoles: hooks.roles,
  useAPIAccessGrants: hooks.grants,
  useCreateAPIRole: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAPIRole: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteAPIRole: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCreateAPIRoleBinding: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAPIRoleBinding: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteAPIRoleBinding: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteAPIAccessGrant: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useReplaceAPIAccessGrant: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

describe("APIAccessPage", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams();
    state.capability = { data: { generic_api: { access: true } }, error: null, isLoading: false, isPending: false };
    navigation.replace.mockReset();
    hooks.roles.mockReset(); hooks.roles.mockImplementation(() => ({ data: { data: [], total: 0, page: 1, page_size: 20 }, error: null, isLoading: false }));
    hooks.grants.mockReset(); hooks.grants.mockImplementation(() => ({ data: { data: [], total: 0, page: 1, page_size: 20 }, error: null, isLoading: false }));
  });

  it("only enables the active tab list hook and restores roles from the URL", () => {
    render(<APIAccessPage />);
    expect(hooks.roles).not.toHaveBeenCalled();
    expect(hooks.grants).toHaveBeenCalledWith({ page: 1, page_size: 20 }, { enabled: true });

    state.searchParams = new URLSearchParams("tab=roles&search=weather&page=2&page_size=50");
    hooks.roles.mockClear(); hooks.grants.mockClear();
    render(<APIAccessPage />);
    expect(hooks.roles).toHaveBeenCalledWith({ page: 2, page_size: 50, search: "weather" }, { enabled: true });
    expect(hooks.grants).not.toHaveBeenCalled();
  });

  it("defaults to the grants tab without writing a redundant URL value", () => {
    render(<APIAccessPage />);

    expect(screen.getByRole("tab", { name: "roles" })).toHaveAttribute("data-state", "inactive");
    expect(screen.getByRole("tab", { name: "grants" })).toHaveAttribute("data-state", "active");
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("switches to roles with one replace and clears the page", async () => {
    state.searchParams = new URLSearchParams("page=3");
    const user = userEvent.setup();
    render(<APIAccessPage />);
    navigation.replace.mockClear();

    await user.click(screen.getByRole("tab", { name: "roles" }));

    expect(navigation.replace.mock.calls).toEqual([["/api-access?tab=roles"]]);
  });

  it("repairs an unknown tab to grants", async () => {
    state.searchParams = new URLSearchParams("tab=unsupported&page=2");
    render(<APIAccessPage />);

    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-access"));
  });

  it("keeps capability pending separate from unavailable", () => {
    state.capability = { data: undefined, error: null, isLoading: true, isPending: true };
    const { container, rerender } = render(<APIAccessPage />);
    expect(container.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
    expect(screen.queryByText("unavailable")).not.toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);

    state.capability = { data: { generic_api: { access: false } }, error: null, isLoading: false, isPending: false };
    rerender(<APIAccessPage />);
    expect(screen.getByText("unavailable")).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });
});
