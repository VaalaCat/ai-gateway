import { renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { tokenAdapter } from "./token";

const state = vi.hoisted(() => ({
  userId: 7,
  params: vi.fn(),
}));

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ user: { user_id: state.userId } }),
}));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: (params: unknown) => {
    state.params(params);
    return { data: { data: [] }, isLoading: false };
  },
  useToken: () => ({ data: undefined }),
}));

beforeEach(() => state.params.mockReset());

it("uses an explicit owner user instead of the current admin scope", () => {
  renderHook(() => tokenAdapter.useList({
    search: "prod",
    scope: "all",
    page_size: 50,
    ownerUserId: 42,
  }));

  expect(state.params).toHaveBeenCalledWith({ search: "prod", page_size: 50, user_id: 42 });
});

it("keeps all-scope token queries unfiltered when no owner is selected", () => {
  renderHook(() => tokenAdapter.useList({ search: "", scope: "all", page_size: 50 }));

  expect(state.params.mock.calls[0][0]).not.toHaveProperty("user_id");
});

it("keeps self-scope behavior for ordinary token pickers", () => {
  renderHook(() => tokenAdapter.useList({ search: "", scope: "self", page_size: 50 }));

  expect(state.params).toHaveBeenCalledWith({ search: "", page_size: 50, user_id: 7 });
});
