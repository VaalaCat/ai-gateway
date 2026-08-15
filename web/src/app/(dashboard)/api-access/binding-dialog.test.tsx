import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { BindingDialog } from "./_components/binding-dialog";

const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
const state = vi.hoisted(() => ({ selectedRole: { data: { id: 3, key: "reader", name: "Reader", description: "", status: 1, built_in: false, permissions: [] }, isLoading: false } as Record<string, unknown> }));
const hooks = vi.hoisted(() => ({ role: vi.fn() }));
beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, entity, value, onChange, disabled }: { id: string; entity: string; value: string; onChange: (value: string) => void; disabled?: boolean }) => <select id={id} data-entity={entity} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}><option value="" /><option value="0">candidate-0</option><option value="8">candidate-8</option><option value="3">candidate-3</option><option value="4">candidate-4</option><option value="101">candidate-101</option></select>,
}));
vi.mock("@/lib/api/api-access", () => ({
  useAPIRole: hooks.role,
  useCreateAPIRoleBinding: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateAPIRoleBinding: () => ({ mutateAsync: mutations.update, isPending: false }),
}));

describe("BindingDialog", () => {
  beforeEach(() => { state.selectedRole = { data: { id: 3, key: "reader", name: "Reader", description: "", status: 1, built_in: false, permissions: [] }, isLoading: false }; hooks.role.mockReset(); hooks.role.mockImplementation(() => state.selectedRole); mutations.create.mockReset(); mutations.create.mockResolvedValue(undefined); mutations.update.mockReset(); mutations.update.mockResolvedValue(undefined); });

  it.each([["user", "user"], ["user_group", "user-group"], ["token", "token"]] as const)("maps %s to the %s searchable principal picker", async (principalType, entity) => {
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    if (principalType !== "user") await choosePrincipalType(user, principalType);
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveAttribute("data-entity", entity);
  });

  it("clears the selected principal after its type changes", async () => {
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={{ id: 5, principal_type: "user", principal_id: 8, role_id: 3 }} />);
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("8");
    await choosePrincipalType(user, "token");
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("");
  });

  it("queries each selected role id and resets its draft when a binding is reopened", () => {
    const { rerender } = render(<BindingDialog open onOpenChange={vi.fn()} binding={{ id: 5, principal_type: "user", principal_id: 8, role_id: 3 }} />);
    expect(hooks.role).toHaveBeenLastCalledWith(3, { enabled: true });
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("8");

    rerender(<BindingDialog open onOpenChange={vi.fn()} binding={{ id: 6, principal_type: "token", principal_id: 4, role_id: 101 }} />);
    expect(hooks.role).toHaveBeenLastCalledWith(101, { enabled: true });
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("4");
    expect(screen.getByRole("combobox", { name: "role" })).toHaveValue("101");
  });

  it("disables save until nonzero principal and role ids are selected", () => {
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it("restores current binding props after cancel and a new open", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { rerender } = render(<BindingDialog open onOpenChange={onOpenChange} binding={{ id: 5, principal_type: "user", principal_id: 8, role_id: 3 }} />);
    await choosePrincipalType(user, "token");
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);

    rerender(<BindingDialog open={false} onOpenChange={onOpenChange} binding={{ id: 5, principal_type: "user", principal_id: 8, role_id: 3 }} />);
    rerender(<BindingDialog open onOpenChange={onOpenChange} binding={{ id: 6, principal_type: "token", principal_id: 4, role_id: 101 }} />);
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveValue("4");
    expect(screen.getByRole("combobox", { name: "role" })).toHaveValue("101");
  });

  it.each([["0", "3"], ["8", "0"], ["8", ""]] as const)("does not submit zero or empty entity ids (%s, %s)", async (principalID, roleID) => {
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    await user.selectOptions(screen.getByRole("combobox", { name: "principal" }), principalID);
    await user.selectOptions(screen.getByRole("combobox", { name: "role" }), roleID);
    const save = screen.getByRole("button", { name: "save" });
    if (!principalID || !roleID) expect(save).toBeDisabled();
    else await user.click(save);
    expect(mutations.create).not.toHaveBeenCalled();
  });

  it("submits only an enabled custom role and keeps a rejected dialog open", async () => {
    const onOpenChange = vi.fn();
    mutations.create.mockRejectedValueOnce(new Error("binding rejected"));
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={onOpenChange} binding={null} />);
    await user.selectOptions(screen.getByRole("combobox", { name: "principal" }), "8");
    await user.selectOptions(screen.getByRole("combobox", { name: "role" }), "3");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith({ principal_type: "user", principal_id: 8, role_id: 3 });
    expect(await screen.findByRole("alert")).toHaveTextContent("binding rejected");
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("rejects disabled or built-in role details even when the picker returns that ID", async () => {
    state.selectedRole = { data: { id: 4, key: "admin", name: "Admin", description: "", status: 1, built_in: true, permissions: [] }, isLoading: false };
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    await user.selectOptions(screen.getByRole("combobox", { name: "principal" }), "8");
    await user.selectOptions(screen.getByRole("combobox", { name: "role" }), "4");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("invalidBinding");
  });

  it("rejects a role detail response that does not match the selected role id", async () => {
    state.selectedRole = { data: { id: 3, key: "reader", name: "Reader", description: "", status: 1, built_in: false, permissions: [] }, isLoading: false };
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    await user.selectOptions(screen.getByRole("combobox", { name: "principal" }), "8");
    await user.selectOptions(screen.getByRole("combobox", { name: "role" }), "101");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("invalidBinding");
  });

  it.each([
    [{ data: { id: 101, key: "late", name: "Late page", description: "", status: 1, built_in: false, permissions: [] }, isLoading: false }, true],
    [{ data: { id: 101, key: "disabled", name: "Disabled", description: "", status: 0, built_in: false, permissions: [] }, isLoading: false }, false],
    [{ data: undefined, isLoading: true }, false],
    [{ data: undefined, isLoading: false }, false],
  ] as const)("uses selected role detail truth rather than a first-page role snapshot", async (selectedRole, allowed) => {
    state.selectedRole = selectedRole;
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);
    await user.selectOptions(screen.getByRole("combobox", { name: "principal" }), "8");
    await user.selectOptions(screen.getByRole("combobox", { name: "role" }), "101");
    await user.click(screen.getByRole("button", { name: "save" }));
    if (allowed) expect(mutations.create).toHaveBeenCalledWith({ principal_type: "user", principal_id: 8, role_id: 101 });
    else expect(screen.getByRole("alert")).toHaveTextContent("invalidBinding");
  });
});

async function choosePrincipalType(user: ReturnType<typeof userEvent.setup>, value: "user_group" | "token") {
  await user.click(screen.getByRole("combobox", { name: "principalType" }));
  await user.click(await screen.findByRole("option", { name: `principalTypeOptions.${value}` }));
}
