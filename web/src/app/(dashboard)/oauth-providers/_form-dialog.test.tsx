import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { OAuthProvider } from "@/lib/types-oauth";
import { ProviderFormDialog } from "./_form-dialog";

const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/oauth", () => ({
  useCreateOAuthProvider: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateOAuthProvider: () => ({ mutateAsync: mutations.update, isPending: false }),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, entity, value, onChange, disabled }: { id: string; entity: string; value: string; onChange: (value: string) => void; disabled?: boolean }) => (
    <select id={id} data-entity={entity} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
      <option value="" />
      <option value="1">Default</option>
      <option value="7">Selected</option>
    </select>
  ),
}));

const provider: OAuthProvider = {
  id: 12, name: "demo", display_name: "Demo", protocol: "oidc", enabled: true,
  authorization_endpoint: "https://idp.example/a", token_endpoint: "https://idp.example/t",
  userinfo_endpoint: "https://idp.example/u", client_id: "client", client_secret: "***",
  default_group_id: 7, created_at: 1, updated_at: 2,
};

describe("ProviderFormDialog registration group", () => {
  beforeEach(() => {
    mutations.create.mockReset().mockResolvedValue(undefined);
    mutations.update.mockReset().mockResolvedValue(undefined);
  });

  it("defaults new provider registration to the system group", async () => {
    const user = userEvent.setup();
    render(<ProviderFormDialog mode="create" open onOpenChange={() => {}} />);
    expect(screen.getByLabelText("defaultGroup")).toHaveValue("1");
    expect(screen.getByLabelText("defaultGroup")).toHaveAttribute("data-entity", "user-group");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ default_group_id: 1 }));
  });

  it("sends a selected group as a numeric create field", async () => {
    const user = userEvent.setup();
    render(<ProviderFormDialog mode="create" open onOpenChange={() => {}} />);
    await user.selectOptions(screen.getByLabelText("defaultGroup"), "7");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ default_group_id: 7 }));
  });

  it("loads and updates the configured group without readonly fields or a blank secret", async () => {
    const user = userEvent.setup();
    render(<ProviderFormDialog mode="edit" open initial={provider} onOpenChange={() => {}} />);
    expect(screen.getByLabelText("defaultGroup")).toHaveValue("7");
    await user.selectOptions(screen.getByLabelText("defaultGroup"), "1");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.update).toHaveBeenCalledWith(expect.objectContaining({ id: 12, default_group_id: 1 }));
    const body = mutations.update.mock.calls[0][0];
    for (const key of ["name", "protocol", "client_secret", "created_at", "updated_at"]) {
      expect(body).not.toHaveProperty(key);
    }
  });

  it("does not submit a cleared group", async () => {
    const user = userEvent.setup();
    render(<ProviderFormDialog mode="create" open onOpenChange={() => {}} />);
    await user.selectOptions(screen.getByLabelText("defaultGroup"), "");
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
  });
});
