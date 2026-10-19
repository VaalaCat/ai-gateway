import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import type { ReceivedAnnouncement } from "./announcements";
import { useAnnouncements, useMarkAnnouncementSeen, usePublicAnnouncements } from "./announcements";
import { ApiError } from "./client";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<{ ApiError: typeof ApiError }>();
  return { ApiError: actual.ApiError, api: { get, post } };
});

const announcement: ReceivedAnnouncement = { id: 11, title: "News", content: "Plain text", group_ids: [], show_for_guests: true, pinned: false, revision: 1, created_at: 1, updated_at: 1, seen: false };

describe("announcement API hooks", () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue([announcement]);
    post.mockReset().mockResolvedValue({ status: "ok" });
  });

  it("does not fetch private announcements without a user or disabled public announcements", () => {
    renderHook(() => ({ privateQuery: useAnnouncements(), publicQuery: usePublicAnnouncements(false) }), { wrapper: queryClientWrapper() });
    expect(get).not.toHaveBeenCalled();
  });

  it("isolates received announcement cache by user identity", () => {
    const qc = createTestQueryClient();
    qc.setQueryDefaults(["announcements"], { staleTime: Infinity });
    qc.setQueryData(["announcements", "received", 1], [announcement]);
    qc.setQueryData(["announcements", "received", 2], [{ ...announcement, id: 22 }]);
    const { result, rerender } = renderHook(({ userId }) => useAnnouncements(userId), { initialProps: { userId: 1 }, wrapper: queryClientWrapper(qc) });
    expect(result.current.data?.[0].id).toBe(11);
    rerender({ userId: 2 });
    expect(result.current.data?.[0].id).toBe(22);
  });


  it("marks only the matching announcement in that user's received cache", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(["announcements", "received", 1], [announcement, { ...announcement, id: 12 }]);
    qc.setQueryData(["announcements", "received", 2], [announcement]);
    const { result } = renderHook(() => useMarkAnnouncementSeen(1), { wrapper: queryClientWrapper(qc) });
    await act(async () => { await result.current.mutateAsync({ id: 11, revision: 1 }); });
    expect(qc.getQueryData<ReceivedAnnouncement[]>(["announcements", "received", 1])?.[0].seen).toBe(true);
    expect(qc.getQueryData<ReceivedAnnouncement[]>(["announcements", "received", 2])?.[0].seen).toBe(false);
    expect(qc.getQueryData<ReceivedAnnouncement[]>(["announcements", "received", 1])?.[1].seen).toBe(false);
  });

  it("does not mark a newer revision seen when an older request finishes", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(["announcements", "received", 1], [announcement]);
    let resolve!: (value: { status: string }) => void;
    const promise = new Promise<{ status: string }>((done) => { resolve = done; });
    post.mockReturnValue(promise);
    const { result } = renderHook(() => useMarkAnnouncementSeen(1), { wrapper: queryClientWrapper(qc) });
    await act(async () => {
      const saving = result.current.mutateAsync({ id: 11, revision: 1 });
      qc.setQueryData(["announcements", "received", 1], [{ ...announcement, revision: 2 }]);
      resolve({ status: "ok" });
      await saving;
    });
    expect(qc.getQueryData<ReceivedAnnouncement[]>(["announcements", "received", 1])?.[0]).toMatchObject({ revision: 2, seen: false });
  });

  it("refreshes the received feed when acknowledging a stale revision conflicts", async () => {
    const qc = createTestQueryClient();
    qc.setQueryDefaults(["announcements"], { staleTime: Infinity });
    qc.setQueryData(["announcements", "received", 1], [announcement]);
    get.mockResolvedValue([{ ...announcement, revision: 2 }]);
    const conflict = new ApiError(409, "Announcement changed");
    post.mockRejectedValue(conflict);
    const { result } = renderHook(() => ({ feed: useAnnouncements(1), seen: useMarkAnnouncementSeen(1) }), { wrapper: queryClientWrapper(qc) });
    await act(async () => {
      await expect(result.current.seen.mutateAsync({ id: 11, revision: 1 })).rejects.toBe(conflict);
    });
    await waitFor(() => expect(result.current.feed.data?.[0]).toMatchObject({ revision: 2, seen: false }));
  });

});
