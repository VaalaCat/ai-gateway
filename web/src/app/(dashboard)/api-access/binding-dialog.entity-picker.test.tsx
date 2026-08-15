import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { BindingDialog } from "./_components/binding-dialog";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const emptyQuery = { data: { data: [], total: 0, page: 1, page_size: 20 }, isLoading: false, isError: false, refetch: vi.fn() };
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", async () => {
  const { useState } = await import("react");
  return {
    useAuth: () => {
      const [user] = useState({ user_id: 1 });
      return { user, isAdmin: false };
    },
  };
});
vi.mock("@/lib/api/users", () => ({ useUsers: () => emptyQuery, useUser: () => ({ data: undefined }) }));
vi.mock("@/lib/api/tokens", () => ({ useTokens: () => emptyQuery, useToken: () => ({ data: undefined }) }));
vi.mock("@/lib/api/api-access", () => ({
  useAPIRole: () => ({ data: undefined, isLoading: false }),
  useAPIRoles: () => emptyQuery,
  useCreateAPIRoleBinding: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAPIRoleBinding: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

describe("BindingDialog principal EntityPicker", () => {
  it("remounts the real picker safely across user to token to user adapter changes", async () => {
    const user = userEvent.setup();
    render(<BindingDialog open onOpenChange={vi.fn()} binding={null} />);

    await user.click(screen.getByRole("combobox", { name: "principalType" }));
    await user.click(await screen.findByRole("option", { name: "principalTypeOptions.token" }));
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveAttribute("id", "api-binding-principal");

    await user.click(screen.getByRole("combobox", { name: "principalType" }));
    await user.click(await screen.findByRole("option", { name: "principalTypeOptions.user" }));
    expect(screen.getByRole("combobox", { name: "principal" })).toHaveAttribute("id", "api-binding-principal");
  });
});
