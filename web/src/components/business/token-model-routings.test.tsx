import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ModelRouting, Token } from "@/lib/types";
import { TokenModelRoutings } from "./token-model-routings";

const { state, toastError, toastSuccess } = vi.hoisted(() => ({
  state: {
    query: {} as Record<string, unknown>,
    deleteRouting: vi.fn(),
  },
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));
vi.mock("@/lib/api/model-routings", () => ({
  useModelRoutings: () => state.query,
  useDeleteModelRouting: () => ({
    mutateAsync: state.deleteRouting,
    isPending: false,
  }),
}));
vi.mock("sonner", () => ({
  toast: { error: toastError, success: toastSuccess },
}));
vi.mock("next-intl", () => ({
  useTranslations: (namespace: string) => (
    key: string,
    values?: Record<string, string | number>,
  ) => {
    const labels: Record<string, string> = {
      "common.cancel": "Cancel",
      "common.confirm": "Confirm",
      "common.delete": "Delete",
      "common.disabled": "Disabled",
      "common.edit": "Edit",
      "common.enabled": "Enabled",
      "common.error": "Error",
      "common.success": "Success",
      "tokenDetail.modelRoutings": "Model routings",
      "tokenDetail.newRouting": "New routing",
      "tokenDetail.retry": "Retry",
      "tokenDetail.routingDeleteDescription": `Delete ${values?.name ?? ""}`,
      "tokenDetail.routingDeleteTitle": "Delete token routing",
      "tokenDetail.routingEmpty": "No token model routings",
      "tokenDetail.routingEmptyDescription": "Add a routing",
      "tokenDetail.routingLoadFailed": "Failed to load model routings",
      "tokenDetail.routingLoading": "Loading model routings",
      "tokenDetail.routingMembers": `${values?.count ?? 0} members`,
      "tokenDetail.routingActions": `Actions for ${values?.name ?? ""}`,
    };
    return labels[`${namespace}.${key}`] ?? key;
  },
}));

const token: Token = {
  id: 23,
  user_id: 7,
  key: "sk-test",
  name: "production",
  status: 1,
  expired_at: 0,
  models: "",
  trace_enabled: false,
  created_at: 1,
  updated_at: 1,
};

function routing(overrides: Partial<ModelRouting>): ModelRouting {
  return {
    id: 1,
    name: "primary",
    scope: "token",
    user_id: 0,
    token_id: token.id,
    members: [{ ref: "gpt-4.1", priority: 0, weight: 1 }],
    enabled: true,
    remark: "",
    created_at: 1,
    updated_at: 1,
    ...overrides,
  };
}

describe("TokenModelRoutings", () => {
  beforeEach(() => {
    state.deleteRouting.mockReset();
    state.deleteRouting.mockResolvedValue(undefined);
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("shows nested routings and keeps create, edit, and delete in the token context", async () => {
    const refetch = vi.fn();
    state.query = {
      data: {
        data: [
          routing({ members: JSON.stringify([
            { ref: "gpt-4.1", priority: 0, weight: 1 },
            { ref: "gpt-4o", priority: 1, weight: 1 },
          ]) }),
          routing({ id: 2, name: "fallback", enabled: false }),
        ],
        total: 2,
      },
      isLoading: false,
      isError: false,
      refetch,
    };

    render(<TokenModelRoutings token={token} />);

    expect(screen.getByRole("link", { name: "New routing" })).toHaveAttribute(
      "href",
      "/model-routings/new?token_id=23",
    );
    expect(screen.getByText("2 members")).toBeInTheDocument();
    expect(screen.getByText("Disabled")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Actions for primary" }));
    expect(await screen.findByRole("menuitem", { name: "Edit" })).toHaveAttribute(
      "href",
      "/model-routings/edit?id=1&token_id=23",
    );
    await userEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(state.deleteRouting).toHaveBeenCalledWith(1));
    expect(toastSuccess).toHaveBeenCalledWith("Success");
  });

  it("keeps the error bounded and retries only when requested", async () => {
    const refetch = vi.fn();
    state.query = {
      data: undefined,
      isLoading: false,
      isError: true,
      refetch,
    };

    render(<TokenModelRoutings token={token} />);
    expect(screen.getByText("Failed to load model routings")).toBeInTheDocument();
    expect(refetch).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("renders a compact empty state without hiding the create action", () => {
    state.query = {
      data: { data: [], total: 0 },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };

    render(<TokenModelRoutings token={token} />);

    expect(screen.getByText("No token model routings")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New routing" })).toBeVisible();
    expect(screen.queryByRole("button", { name: /Actions for/ })).not.toBeInTheDocument();
  });
});
