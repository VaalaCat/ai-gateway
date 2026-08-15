import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { permissionValidationError, RoleForm } from "./_components/role-form";

const state = vi.hoisted(() => ({ role: { data: undefined, error: null, isLoading: false } as Record<string, unknown>, capability: { data: { generic_api: { access: true } }, error: null, isLoading: false, isPending: false } as Record<string, unknown> }));
const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
const navigation = vi.hoisted(() => ({ push: vi.fn() }));
const hooks = vi.hoisted(() => ({ role: vi.fn() }));
const notifications = vi.hoisted(() => ({ success: vi.fn() }));
beforeAll(() => { Element.prototype.scrollIntoView ??= () => {}; });
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => navigation }));
vi.mock("sonner", () => ({ toast: notifications }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", () => ({ useCapabilities: () => state.capability }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) => <select id={id} value={value} onChange={(event) => onChange(event.target.value)}><option value="" /><option value="7">candidate-7</option><option value="9">candidate-9</option></select>,
}));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: ({ entity, value, onChange }: { entity: string; value: string[]; onChange: (value: string[]) => void }) => (
    <select
      multiple
      aria-label={`members-${entity}`}
      value={value}
      onChange={(event) => onChange(Array.from(event.currentTarget.selectedOptions, (option) => option.value))}
    >
      <option value="7">candidate-7</option>
      <option value="8">candidate-8</option>
      <option value="9">candidate-9</option>
    </select>
  ),
}));
vi.mock("@/lib/api/api-services", () => ({ useAPIRoute: () => ({ data: undefined, error: null }) }));
vi.mock("@/lib/api/api-access", () => ({
  useAPIRole: hooks.role,
  useCreateAPIRole: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateAPIRole: () => ({ mutateAsync: mutations.update, isPending: false }),
}));

describe("RoleForm", () => {
  beforeEach(() => {
    state.role = { data: undefined, error: null, isLoading: false };
    state.capability = { data: { generic_api: { access: true } }, error: null, isLoading: false, isPending: false };
    hooks.role.mockReset(); hooks.role.mockImplementation(() => state.role);
    mutations.create.mockReset(); mutations.create.mockResolvedValue(undefined);
    mutations.update.mockReset(); mutations.update.mockResolvedValue(undefined);
    navigation.push.mockReset();
    notifications.success.mockReset();
  });

  it.each([
    [{ rowKey: 1, resource: "api_service", resource_id: 0, action: "invoke", scope: "specific" }, "permissionTargetRequired"],
    [{ rowKey: 1, resource: "api_route", resource_id: 0, action: "invoke", scope: "all" }, "permissionTargetRequired"],
    [{ rowKey: 1, resource: "api_route", resource_id: 7, action: "invoke", scope: "specific" }, "permissionServiceRequired"],
    [{ rowKey: 1, resource: "api_service", resource_id: -1, action: "invoke", scope: "specific" }, "permissionTargetRequired"],
    [{ rowKey: 1, resource: "api_service", resource_id: Number.MAX_SAFE_INTEGER + 1, action: "invoke", scope: "specific" }, "permissionTargetRequired"],
    [{ rowKey: 1, resource: "api_service", resource_id: Number.MAX_SAFE_INTEGER, action: "invoke", scope: "specific" }, undefined],
    [{ rowKey: 1, resource: "api_route", resource_id: 7, apiServiceId: 9, action: "invoke", scope: "specific" }, undefined],
  ] as const)("validates a specific permission target before mutation", (row, expected) => {
    expect(permissionValidationError([row])).toBe(expected);
  });

  it.each([
    [{ data: undefined, isLoading: true, isPending: true, error: null }, false],
    [{ data: { generic_api: { access: false } }, isLoading: false, isPending: false, error: null }, false],
    [{ data: undefined, isLoading: false, isPending: false, error: { status: 403 } }, false],
    [{ data: { generic_api: { access: true } }, isLoading: false, isPending: false, error: null }, true],
  ] as const)("only enables edit detail query after capability access succeeds", (capability, enabled) => {
    state.capability = capability;
    render(<RoleForm mode={{ kind: "edit", id: 7 }} />);
    expect(hooks.role).toHaveBeenCalledWith(7, { enabled });
  });

  it("keeps exactly one page heading while role capability is loading", () => {
    state.capability = { data: undefined, isLoading: true, isPending: true, error: null };

    render(<RoleForm mode={{ kind: "create" }} />);

    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });

  it("saves an empty permission list without exposing a numeric resource id field", async () => {
    const user = userEvent.setup();
    render(<RoleForm mode={{ kind: "create" }} />);
    const save = screen.getByRole("button", { name: "save" });
    expect(save).toHaveAttribute("form", "api-role-form");
    expect(save.closest("[data-slot=page-layout-footer]")).toBeInTheDocument();
    await user.type(screen.getByLabelText("key"), "reader");
    await user.type(screen.getByLabelText("name"), "Reader");
    await user.click(save);
    expect(mutations.create).toHaveBeenCalledWith({ key: "reader", name: "Reader", description: "", status: 1, permissions: [], members: [] });
    expect(notifications.success).toHaveBeenCalledWith("success");
    expect(screen.queryByLabelText("resourceId")).not.toBeInTheDocument();
  });

  it("adds an all-resources permission draft whose UI does not show zero", async () => {
    const user = userEvent.setup();
    render(<RoleForm mode={{ kind: "create" }} />);
    await user.click(screen.getByRole("button", { name: "addPermission" }));
    expect(screen.getByText("allResources")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("0")).not.toBeInTheDocument();
  });

  it("keeps a specific permission draft and skips mutation until its target is selected", async () => {
    const user = userEvent.setup();
    render(<RoleForm mode={{ kind: "create" }} />);
    await user.type(screen.getByLabelText("key"), "reader");
    await user.type(screen.getByLabelText("name"), "Reader");
    await user.click(screen.getByRole("button", { name: "addPermission" }));
    await user.click(screen.getByRole("radio", { name: "specificResource" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("permissionTargetRequired");
    expect(screen.getByRole("radio", { name: "specificResource" })).toHaveAttribute("data-state", "on");

    await user.selectOptions(screen.getByRole("combobox", { name: "specificResource" }), "7");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith({ key: "reader", name: "Reader", description: "", status: 1, permissions: [{ resource: "api_service", resource_id: 7, action: "invoke" }], members: [] });
  });

  it("preserves all member types when one member group changes during edit", async () => {
    state.role = { data: { id: 6, key: "operators", name: "Operators", description: "", status: 1, permissions: [], members: [
      { principal_type: "user", principal_id: 7 },
      { principal_type: "user_group", principal_id: 8 },
      { principal_type: "token", principal_id: 9 },
    ] }, error: null, isLoading: false };
    const user = userEvent.setup();
    render(<RoleForm mode={{ kind: "edit", id: 6 }} />);

    expect(screen.getByLabelText("members-user")).toHaveValue(["7"]);
    expect(screen.getByLabelText("members-user-group")).toHaveValue(["8"]);
    expect(screen.getByLabelText("members-api-access-token")).toHaveValue(["9"]);

    await user.selectOptions(screen.getByLabelText("members-user"), ["7", "8"]);
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 6, members: [
      { principal_type: "user", principal_id: 7 },
      { principal_type: "user", principal_id: 8 },
      { principal_type: "user_group", principal_id: 8 },
      { principal_type: "token", principal_id: 9 },
    ] }));
  });

  it("fails closed for an existing built-in role", () => {
    state.role = { data: { id: 4, key: "admin", name: "Admin", description: "", built_in: true, status: 1, permissions: [], members: [] }, error: null, isLoading: false };
    render(<RoleForm mode={{ kind: "edit", id: 4 }} />);
    expect(screen.getByText("builtInProtected")).toBeInTheDocument();
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
  });

  it("fails closed for gateway_admin even when built_in is false", () => {
    state.role = { data: { id: 5, key: "gateway_admin", name: "Gateway admin", description: "", built_in: false, status: 1, permissions: [], members: [] }, error: null, isLoading: false };
    render(<RoleForm mode={{ kind: "edit", id: 5 }} />);
    expect(screen.getByText("builtInProtected")).toBeInTheDocument();
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
  });

  it("keeps the draft and a server failure visible", async () => {
    mutations.create.mockRejectedValueOnce(new Error("duplicate permission"));
    const user = userEvent.setup();
    render(<RoleForm mode={{ kind: "create" }} />);
    await user.type(screen.getByLabelText("key"), "reader");
    await user.type(screen.getByLabelText("name"), "Reader");
    await user.click(screen.getByRole("button", { name: "save" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("duplicate permission");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(screen.getByLabelText("key")).toHaveValue("reader");
  });
});
