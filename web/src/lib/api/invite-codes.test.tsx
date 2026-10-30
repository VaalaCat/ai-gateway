import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import { useCreateInviteCode } from "./invite-codes";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("./client", () => ({ api: { post }, buildQuery: () => "" }));

beforeEach(() => post.mockReset().mockResolvedValue({ id: 1, group_id: 8 }));

it("posts an administrator's selected group and invalidates both invite lists", async () => {
  const qc = createTestQueryClient();
  const invalidate = vi.spyOn(qc, "invalidateQueries");
  const { result } = renderHook(() => useCreateInviteCode(), { wrapper: queryClientWrapper(qc) });
  await act(async () => { await result.current.mutateAsync({ max_uses: 2, group_id: 8 }); });
  expect(post).toHaveBeenCalledWith("/invite-codes", { max_uses: 2, group_id: 8 });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["invite-codes"] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["admin-invite-codes"] });
});

it("does not inject group_id into ordinary user requests", async () => {
  const { result } = renderHook(() => useCreateInviteCode(), { wrapper: queryClientWrapper() });
  await act(async () => { await result.current.mutateAsync({ max_uses: 1 }); });
  expect(post).toHaveBeenCalledWith("/invite-codes", { max_uses: 1 });
  expect(post.mock.calls[0][1]).not.toHaveProperty("group_id");
});
