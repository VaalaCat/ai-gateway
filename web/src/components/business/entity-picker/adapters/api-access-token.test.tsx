import { renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { apiAccessTokenAdapter } from "./api-access-token";

const calls = vi.hoisted(() => vi.fn());

vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 7 } }) }));
vi.mock("@/lib/api/tokens", () => ({
  useTokens: (params: unknown, options: unknown) => {
    calls(params, options);
    return { data: { data: [] }, isLoading: false };
  },
}));

it("lists only explicit API-role Tokens for standalone grants", () => {
  calls.mockReset();
  renderHook(() => apiAccessTokenAdapter.useList({ search: "grant", scope: "self", page_size: 50 }));

  expect(calls).toHaveBeenCalledWith({ search: "grant", page_size: 50, api_role_mode: "explicit", user_id: 7 }, { enabled: true });
});
