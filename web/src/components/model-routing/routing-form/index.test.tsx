import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { RoutingForm } from ".";

const { createRouting, push, state, updateRouting } = vi.hoisted(() => ({
  createRouting: vi.fn(),
  push: vi.fn(),
  state: {
    tokenLoading: false,
  },
  updateRouting: vi.fn(),
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("@/hooks/use-user-pref", () => ({
  useUserPref: () => ["", vi.fn()],
}));
vi.mock("@/lib/api/tokens", () => ({
  useToken: () => ({
    data: state.tokenLoading ? undefined : { id: 23, key: "sk-token" },
    isLoading: state.tokenLoading,
  }),
  useTokens: () => ({ data: { data: [] }, isLoading: false }),
}));
vi.mock("@/lib/api/model-routings", () => ({
  ROUTING_ERROR_KEYS: {},
  useRoutingCandidatesByToken: () => ({ data: { visible_refs: ["gpt-4.1"] } }),
}));
vi.mock("./use-routing-form", async () => {
  const { useForm } = await import("react-hook-form");
  return {
    useRoutingForm: () => ({
      form: useForm({
        defaultValues: {
          name: "smart",
          scope: "token",
          user_id: 0,
          members: [{ ref: "gpt-4.1", priority: 0, weight: 1 }],
          enabled: true,
          remark: "",
        },
      }),
      isLoading: false,
      createMut: { mutateAsync: createRouting },
      updateMut: { mutateAsync: updateRouting },
    }),
  };
});
vi.mock("./sections/basic", () => ({ BasicSection: () => null }));
vi.mock("./sections/members", () => ({ MembersSection: () => null }));
vi.mock("../preview-panel", () => ({ PreviewPanel: () => null }));
vi.mock("./save-bar", () => ({
  SaveBar: ({ onCancel }: { onCancel?: () => void }) => (
    <div>
      <button type="button" onClick={onCancel}>Cancel</button>
      <button type="submit">Save</button>
    </div>
  ),
}));

describe("RoutingForm token owner", () => {
  beforeEach(() => {
    createRouting.mockReset();
    createRouting.mockResolvedValue({});
    updateRouting.mockReset();
    updateRouting.mockResolvedValue({});
    push.mockReset();
    state.tokenLoading = false;
  });

  it("creates through the fixed token form and returns to the expanded token", async () => {
    render(<RoutingForm mode={{ kind: "new" }} apiMode="user" tokenId={23} />);

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(createRouting).toHaveBeenCalledWith(expect.objectContaining({
      name: "smart",
      scope: "token",
      members: [{ ref: "gpt-4.1", priority: 0, weight: 1 }],
    })));
    expect(push).toHaveBeenCalledWith("/tokens?selected=23");
  });

  it("updates the path-owned routing and preserves its return context", async () => {
    render(<RoutingForm mode={{ kind: "edit", id: 44 }} apiMode="admin" tokenId={23} />);

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateRouting).toHaveBeenCalledWith(expect.objectContaining({
      id: 44,
      scope: "token",
    })));
    expect(push).toHaveBeenCalledWith("/tokens?selected=23");
  });

  it("does not expose form actions until the fixed token has loaded", () => {
    state.tokenLoading = true;
    render(<RoutingForm mode={{ kind: "new" }} apiMode="user" tokenId={23} />);

    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });
});
