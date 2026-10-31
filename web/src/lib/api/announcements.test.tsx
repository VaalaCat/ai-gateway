import { act, renderHook, waitFor } from "@testing-library/react";
import { focusManager, onlineManager } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import type { AnnouncementPage, ReceivedAnnouncementSummary } from "./announcements";
import { useAdminAnnouncements, useAnnouncementDetail, useAnnouncements, useCreateAnnouncement, useDeleteAnnouncement, useMarkAnnouncementSeen, usePrefetchAnnouncementDetail, usePrefetchAnnouncementPage, useUpdateAnnouncement } from "./announcements";
import { ApiError } from "./client";

const { get, post, put, remove } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn() }));
vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<{ ApiError: typeof ApiError }>();
  return { ApiError: actual.ApiError, api: { get, post, put, delete: remove } };
});

const announcement: ReceivedAnnouncementSummary = { id: 11, title: "News", group_ids: [], show_for_guests: true, pinned: false, revision: 1, created_at: 1, updated_at: 1, seen: false };
const firstPage = { items: [announcement], total: 1, page: 1, page_size: 10 };
const feedKey = ["announcements", "received", 1, "feed", 1, 10];
type Feed = AnnouncementPage<ReceivedAnnouncementSummary>;

function feedData(items = [announcement]): Feed {
  return { items, total: items.length, page: 1, page_size: 10 };
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, resolve, reject };
}

describe("announcement API hooks", () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue(firstPage);
    post.mockReset().mockResolvedValue({ status: "ok" });
    put.mockReset().mockResolvedValue({ ...announcement, content: "Body" });
    remove.mockReset().mockResolvedValue({ status: "ok" });
  });

  it("does not fetch disabled feeds or unopened details", () => {
    renderHook(() => ({ privateQuery: useAnnouncements(1, false), publicQuery: useAnnouncements(undefined, false), detail: useAnnouncementDetail() }), { wrapper: queryClientWrapper() });
    expect(get).not.toHaveBeenCalled();
  });

  it("waits for auth hydration before loading announcements", async () => {
    const initialProps: { userId: number | undefined; enabled: boolean } = { userId: undefined, enabled: false };
    const { result, rerender } = renderHook(({ userId, enabled }) => useAnnouncements(userId, enabled), { initialProps, wrapper: queryClientWrapper() });
    expect(get).not.toHaveBeenCalled();
    rerender({ userId: 1, enabled: true });
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(11));
    expect(get).toHaveBeenCalledTimes(1);
  });

  it.each(["received", "public", "admin"] as const)("loads %s once despite timers, focus, reconnect, or remount", async (scope) => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const useFeeds = () => ({ received: useAnnouncements(1, scope === "received"), publicFeed: useAnnouncements(undefined, scope === "public"), admin: useAdminAnnouncements(1, 10, 1) });
    // Admin has its own initial read in all cases; both feeds must remain quiet afterwards.
    const first = renderHook(useFeeds, { wrapper });
    await waitFor(() => expect(first.result.current.admin.isSuccess).toBe(true));
    if (scope !== "admin") await waitFor(() => expect(first.result.current[scope === "public" ? "publicFeed" : "received"].isSuccess).toBe(true));
    const initialCalls = get.mock.calls.length;
    first.unmount();
    vi.useFakeTimers();
    try {
      act(() => {
        focusManager.setFocused(false);
        onlineManager.setOnline(false);
        focusManager.setFocused(true);
        onlineManager.setOnline(true);
        vi.advanceTimersByTime(90_000);
      });
      renderHook(useFeeds, { wrapper });
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(get).toHaveBeenCalledTimes(initialCalls);
    } finally {
      vi.useRealTimers();
      focusManager.setFocused(undefined);
      onlineManager.setOnline(true);
    }
    const fresh = renderHook(useFeeds, { wrapper: queryClientWrapper() });
    await waitFor(() => expect(fresh.result.current.admin.isSuccess).toBe(true));
    expect(get.mock.calls.length).toBeGreaterThan(initialCalls);
  });

  it("does not retry a failed initial read on remount, but offers explicit retry", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    get.mockRejectedValue(new Error("offline"));
    const first = renderHook(() => useAnnouncements(1), { wrapper });
    await waitFor(() => expect(first.result.current.isError).toBe(true));
    first.unmount();
    const second = renderHook(() => useAnnouncements(1), { wrapper });
    expect(second.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(1);
    get.mockResolvedValue(firstPage);
    await act(async () => { await second.result.current.refetch(); });
    await waitFor(() => expect(second.result.current.data?.items).toEqual([announcement]));
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each([1, undefined])("loads each identity separately without exposing another identity's cache", async (initialUserId) => {
    const qc = createTestQueryClient();
    const nextUserId = initialUserId === undefined ? 1 : undefined;
    get.mockResolvedValueOnce(firstPage)
      .mockResolvedValueOnce({ ...firstPage, items: [{ ...announcement, id: 22 }] });
    const { result, rerender } = renderHook(({ userId }: { userId: number | undefined }) => useAnnouncements(userId), { initialProps: { userId: initialUserId }, wrapper: queryClientWrapper(qc) });
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(11));
    rerender({ userId: nextUserId });
    expect(result.current.data).toBeUndefined();
    expect(result.current.loadedItems).toEqual([]);
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(22));
    rerender({ userId: initialUserId });
    expect(result.current.data?.items[0].id).toBe(11);
    rerender({ userId: nextUserId });
    expect(result.current.data?.items[0].id).toBe(22);
    expect(get).toHaveBeenCalledTimes(2);
    const freshPage = renderHook(() => useAnnouncements(nextUserId), { wrapper: queryClientWrapper() });
    await waitFor(() => expect(freshPage.result.current.isSuccess).toBe(true));
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("does not expose an old user's response when it arrives after switching identities", async () => {
    let resolve!: (value: Feed) => void;
    get.mockReturnValueOnce(new Promise<Feed>((done) => { resolve = done; }))
      .mockResolvedValueOnce({ ...firstPage, items: [{ ...announcement, id: 22 }] });
    const { result, rerender } = renderHook(({ userId }) => useAnnouncements(userId), { initialProps: { userId: 1 }, wrapper: queryClientWrapper() });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    rerender({ userId: 2 });
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(22));
    await act(async () => { resolve(firstPage); });
    expect(result.current.loadedItems).toEqual([{ ...announcement, id: 22 }]);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each(["received", "public", "admin"] as const)("jumps directly to a numbered %s page and reuses each cached page", async (scope) => {
    const older = { ...announcement, id: 22 };
    get.mockResolvedValueOnce({ ...firstPage, total: 50 })
      .mockResolvedValueOnce({ items: [older], total: 50, page: 5, page_size: 10 })
      .mockResolvedValueOnce({ items: [older], total: 50, page: 5, page_size: 7 });
    const wrapper = queryClientWrapper();
    const useFeed = scope === "admin"
      ? function useAdminFeed({ page, size }: { page: number; size: number }) { return useAdminAnnouncements(page, size, 1); }
      : function useRecipientFeed({ page, size }: { page: number; size: number }) { return useAnnouncements(scope === "received" ? 1 : undefined, true, page, size); };
    const { result, rerender } = renderHook(useFeed, { initialProps: { page: 1, size: 10 }, wrapper });
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(11));
    rerender({ page: 5, size: 10 });
    await waitFor(() => expect(result.current.data?.items[0].id).toBe(22));
    rerender({ page: 1, size: 10 });
    expect(result.current.data?.items[0].id).toBe(11);
    rerender({ page: 5, size: 10 });
    expect(result.current.data?.items[0].id).toBe(22);
    expect(get).toHaveBeenCalledTimes(2);
    rerender({ page: 5, size: 7 });
    await waitFor(() => expect(result.current.data?.page_size).toBe(7));
    const path = scope === "admin" ? "/admin/announcements" : "/announcements";
    expect(get.mock.calls).toEqual([[`${path}?page=1&page_size=10`], [`${path}?page=5&page_size=10`], [`${path}?page=5&page_size=7`]]);
  });

  it.each(["received", "public", "admin"] as const)("retains the last %s page and total while navigation is pending or fails", async (scope) => {
    const deferred = createDeferred<Feed>();
    const first = { ...firstPage, total: 510 };
    get.mockResolvedValueOnce(first).mockReturnValueOnce(deferred.promise);
    const { result, rerender } = renderHook(({ page }) => scope === "admin"
      ? useAdminAnnouncements(page, 10, 1)
      : useAnnouncements(scope === "received" ? 1 : undefined, true, page),
    { initialProps: { page: 1 }, wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.data).toEqual(first));
    rerender({ page: 2 });
    expect(result.current.data).toEqual(first);
    expect(result.current.total).toBe(510);
    expect(result.current.isPlaceholderData).toBe(true);
    await act(async () => { deferred.reject(new Error("Next page unavailable")); });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toEqual(first);
    expect(result.current.total).toBe(510);
    expect(result.current.isPlaceholderData).toBe(true);
    rerender({ page: 1 });
    expect(result.current.isPlaceholderData).toBe(false);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each(["received", "public", "admin"] as const)("replaces retained %s rows only after the next page succeeds", async (scope) => {
    const deferred = createDeferred<Feed>();
    const second = { ...firstPage, page: 2, total: 51, items: [{ ...announcement, id: 22 }] };
    get.mockResolvedValueOnce({ ...firstPage, total: 51 }).mockReturnValueOnce(deferred.promise);
    const { result, rerender } = renderHook(({ page }) => scope === "admin"
      ? useAdminAnnouncements(page, 10, 1)
      : useAnnouncements(scope === "received" ? 1 : undefined, true, page),
    { initialProps: { page: 1 }, wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.data?.page).toBe(1));
    rerender({ page: 2 });
    expect(result.current.data?.page).toBe(1);
    expect(result.current.total).toBe(51);
    await act(async () => { deferred.resolve(second); });
    await waitFor(() => expect(result.current.data).toEqual(second));
    expect(result.current.isPlaceholderData).toBe(false);
    rerender({ page: 1 });
    rerender({ page: 2 });
    expect(result.current.data).toEqual(second);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each(["received", "admin"] as const)("never retains %s rows across identity or page-size changes", async (scope) => {
    const nextIdentity = createDeferred<Feed>();
    const nextSize = createDeferred<Feed>();
    get.mockResolvedValueOnce(firstPage).mockReturnValueOnce(nextIdentity.promise).mockReturnValueOnce(nextSize.promise);
    const { result, rerender } = renderHook(({ userId, size }) => scope === "admin"
      ? useAdminAnnouncements(1, size, userId)
      : useAnnouncements(userId, true, 1, size),
    { initialProps: { userId: 1, size: 10 }, wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.data?.page).toBe(1));
    rerender({ userId: 2, size: 10 });
    expect(result.current.data).toBeUndefined();
    expect(result.current.total).toBe(0);
    const second = { ...firstPage, items: [{ ...announcement, id: 22 }] };
    await act(async () => { nextIdentity.resolve(second); });
    await waitFor(() => expect(result.current.data).toEqual(second));
    rerender({ userId: 2, size: 20 });
    expect(result.current.data).toBeUndefined();
    expect(result.current.total).toBe(0);
    await act(async () => { nextSize.reject(new Error("Size unavailable")); });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  it.each([1, undefined])("keeps identity %s page hover outside loaded summaries until navigation", async (userId) => {
    const qc = createTestQueryClient();
    const prefix = userId === undefined ? ["announcements", "public"] : ["announcements", "received", userId];
    const first = { ...firstPage, total: 20 };
    const second = { ...first, page: 2, items: [{ ...announcement, id: 22 }] };
    get.mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const { result, rerender } = renderHook(({ page }) => ({ feed: useAnnouncements(userId, true, page), prefetch: usePrefetchAnnouncementPage(userId) }),
      { initialProps: { page: 1 }, wrapper: queryClientWrapper(qc) });
    await waitFor(() => expect(result.current.feed.data).toEqual(first));
    await act(async () => { await result.current.prefetch(2); await result.current.prefetch(2); });
    expect(result.current.feed.loadedItems).toEqual([announcement]);
    expect(qc.getQueryData([...prefix, "feed", 2, 10])).toBeUndefined();
    expect(qc.getQueryData([...prefix, "page-prefetch", 2, 10])).toEqual(second);
    expect(post).not.toHaveBeenCalled();
    rerender({ page: 2 });
    await waitFor(() => expect(result.current.feed.data).toEqual(second));
    expect(result.current.feed.loadedItems).toEqual([announcement, second.items[0]]);
    expect(qc.getQueryData([...prefix, "page-prefetch", 2, 10])).toBeUndefined();
    rerender({ page: 1 });
    await act(async () => { await result.current.prefetch(2); });
    rerender({ page: 2 });
    expect(result.current.feed.data).toEqual(second);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("deduplicates pending page hovers and navigation", async () => {
    const deferred = createDeferred<Feed>();
    const second = { ...firstPage, page: 2 };
    get.mockReturnValueOnce(deferred.promise);
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, { ...firstPage, total: 20 });
    const { result, rerender } = renderHook(({ page }) => ({ feed: useAnnouncements(1, true, page), prefetch: usePrefetchAnnouncementPage(1) }),
      { initialProps: { page: 1 }, wrapper: queryClientWrapper(qc) });
    let hover!: Promise<void>;
    let repeated!: Promise<void>;
    act(() => { hover = result.current.prefetch(2); repeated = result.current.prefetch(2); });
    rerender({ page: 2 });
    expect(result.current.feed.data?.page).toBe(1);
    expect(get).toHaveBeenCalledTimes(1);
    await act(async () => { deferred.resolve(second); await Promise.all([hover, repeated]); });
    await waitFor(() => expect(result.current.feed.data).toEqual(second));
    expect(get).toHaveBeenCalledTimes(1);
  });

  it.each([false, true])("retries failed pending=%s page hover only on navigation", async (pending) => {
    const deferred = createDeferred<Feed>();
    const second = { ...firstPage, page: 2 };
    get.mockReturnValueOnce(deferred.promise).mockResolvedValueOnce(second);
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, { ...firstPage, total: 20 });
    const { result, rerender } = renderHook(({ page }) => ({ feed: useAnnouncements(1, true, page), prefetch: usePrefetchAnnouncementPage(1) }),
      { initialProps: { page: 1 }, wrapper: queryClientWrapper(qc) });
    let hover!: Promise<void>;
    act(() => { hover = result.current.prefetch(2); });
    if (!pending) await act(async () => { deferred.reject(new Error("Hover failed")); await hover; });
    expect(get).toHaveBeenCalledTimes(1);
    rerender({ page: 2 });
    if (pending) await act(async () => { deferred.reject(new Error("Hover failed")); await hover; });
    await waitFor(() => expect(result.current.feed.data).toEqual(second));
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each([false, true])("expires pending=%s page hover after five seconds", async (pending) => {
    const deferred = createDeferred<Feed>();
    const expired = { ...firstPage, page: 2 };
    const fresh = { ...expired, items: [{ ...announcement, id: 22 }] };
    get.mockReturnValueOnce(deferred.promise).mockResolvedValueOnce(fresh);
    const wrapper = queryClientWrapper();
    const hover = renderHook(() => usePrefetchAnnouncementPage(1), { wrapper });
    let prefetch!: Promise<void>;
    act(() => { prefetch = hover.result.current(2); });
    if (!pending) await act(async () => { deferred.resolve(expired); await prefetch; });
    const clock = vi.spyOn(Date, "now").mockReturnValue(Date.now() + 5_001);
    try {
      const opened = renderHook(() => useAnnouncements(1, true, 2), { wrapper });
      await waitFor(() => expect(opened.result.current.data).toEqual(fresh));
      if (pending) await act(async () => { deferred.resolve(expired); await prefetch; });
      expect(opened.result.current.data).toEqual(fresh);
      expect(get).toHaveBeenCalledTimes(2);
    } finally {
      clock.mockRestore();
    }
  });

  it.each(["account", "public", "size", "admin"] as const)("does not consume a page hover across %s boundaries", async (boundary) => {
    const wrapper = queryClientWrapper();
    get.mockResolvedValueOnce({ ...firstPage, page: 2 }).mockRejectedValueOnce(new Error("Unavailable"));
    const hover = renderHook(() => usePrefetchAnnouncementPage(1), { wrapper });
    await act(async () => { await hover.result.current(2); });
    const opened = renderHook(() => boundary === "admin"
      ? useAdminAnnouncements(2, 10, 1)
      : useAnnouncements(boundary === "account" ? 2 : boundary === "public" ? undefined : 1, true, 2, boundary === "size" ? 20 : 10), { wrapper });
    await waitFor(() => expect(opened.result.current.isError).toBe(true));
    expect(opened.result.current.data).toBeUndefined();
    expect(opened.result.current.total).toBe(0);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("keeps a late private page hover isolated after the identity changes", async () => {
    const deferred = createDeferred<Feed>();
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const secondUser = { ...firstPage, page: 2, items: [{ ...announcement, id: 22 }] };
    get.mockReturnValueOnce(deferred.promise).mockResolvedValueOnce(secondUser);
    const hover = renderHook(({ userId }) => usePrefetchAnnouncementPage(userId), { initialProps: { userId: 1 }, wrapper });
    let oldHover!: Promise<void>;
    act(() => { oldHover = hover.result.current(2); });
    hover.rerender({ userId: 2 });
    await act(async () => { await hover.result.current(2); });
    const opened = renderHook(() => useAnnouncements(2, true, 2), { wrapper });
    await waitFor(() => expect(opened.result.current.data).toEqual(secondUser));
    await act(async () => { deferred.resolve({ ...firstPage, page: 2, items: [{ ...announcement, show_for_guests: false }] }); await oldHover; });
    expect(opened.result.current.data).toEqual(secondUser);
    expect(opened.result.current.loadedItems).toEqual(secondUser.items);
    expect(qc.getQueryData(["announcements", "received", 1, "feed", 2, 10])).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("garbage collects unused page hovers without committing their summaries", async () => {
    const qc = createTestQueryClient();
    vi.useFakeTimers();
    try {
      const hover = renderHook(() => usePrefetchAnnouncementPage(1), { wrapper: queryClientWrapper(qc) });
      await act(async () => { await hover.result.current(2); });
      const key = ["announcements", "received", 1, "page-prefetch", 2, 10];
      expect(qc.getQueryData(key)).toBeDefined();
      await act(async () => { await vi.advanceTimersByTimeAsync(5_001); });
      expect(qc.getQueryData(key)).toBeUndefined();
      expect(qc.getQueryData(["announcements", "received", 1, "feed", 2, 10])).toBeUndefined();
      expect(post).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps an opened private body and loaded summaries when the next page fails", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, { ...firstPage, total: 20, items: [{ ...announcement, show_for_guests: false }] });
    get.mockResolvedValueOnce({ ...announcement, show_for_guests: false, content: "Private body" }).mockRejectedValueOnce(new Error("Next page failed"));
    const { result, rerender } = renderHook(({ page }) => ({ feed: useAnnouncements(1, true, page), detail: useAnnouncementDetail(11, 1) }),
      { initialProps: { page: 1 }, wrapper: queryClientWrapper(qc) });
    await waitFor(() => expect(result.current.detail.data?.content).toBe("Private body"));
    rerender({ page: 2 });
    await waitFor(() => expect(result.current.feed.isError).toBe(true));
    expect(result.current.feed.total).toBe(20);
    expect(result.current.feed.data?.page).toBe(1);
    expect(result.current.feed.loadedItems).toEqual([expect.objectContaining({ id: 11, show_for_guests: false })]);
    expect(result.current.detail.data?.content).toBe("Private body");
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("uses committed same-size metadata when mounting an unavailable page", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, { ...firstPage, total: 510 });
    qc.setQueryData(["announcements", "received", 1, "feed", 1, 20], { ...firstPage, total: 999, page_size: 20 });
    qc.setQueryData(["announcements", "received", 2, "feed", 1, 10], { ...firstPage, total: 888 });
    get.mockRejectedValue(new Error("Page unavailable"));
    const { result } = renderHook(() => useAnnouncements(1, true, 2), { wrapper: queryClientWrapper(qc) });
    expect(result.current.total).toBe(510);
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.total).toBe(510);
    expect(result.current.loadedItems).toEqual([announcement]);
  });

  it("marks matching revisions across cached pages locally without fetching any feed", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData<Feed>(feedKey, feedData([{ ...announcement, id: 12 }]));
    const secondPageKey = ["announcements", "received", 1, "feed", 2, 10];
    qc.setQueryData<Feed>(secondPageKey, { ...firstPage, page: 2 });
    const otherUserKey = ["announcements", "received", 2, "feed", 1, 10];
    const publicKey = ["announcements", "public", "feed", 1, 10];
    qc.setQueryData(otherUserKey, feedData());
    qc.setQueryData(publicKey, feedData());
    const { result } = renderHook(() => ({ feed: useAnnouncements(1), seen: useMarkAnnouncementSeen(1) }), { wrapper: queryClientWrapper(qc) });
    await act(async () => { await result.current.seen.mutateAsync({ id: 11, revision: 1 }); });
    expect(qc.getQueryData<Feed>(secondPageKey)?.items[0].seen).toBe(true);
    expect(qc.getQueryData<Feed>(feedKey)?.items[0].seen).toBe(false);
    expect(qc.getQueryData<Feed>(otherUserKey)?.items[0].seen).toBe(false);
    expect(qc.getQueryData<Feed>(publicKey)?.items[0].seen).toBe(false);
    expect(get).not.toHaveBeenCalled();
  });

  it("does not mark a newer revision seen when an older request finishes", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, feedData());
    let resolve!: (value: { status: string }) => void;
    post.mockReturnValue(new Promise<{ status: string }>((done) => { resolve = done; }));
    const { result } = renderHook(() => useMarkAnnouncementSeen(1), { wrapper: queryClientWrapper(qc) });
    await act(async () => {
      const saving = result.current.mutateAsync({ id: 11, revision: 1 });
      qc.setQueryData(feedKey, feedData([{ ...announcement, revision: 2 }]));
      resolve({ status: "ok" });
      await saving;
    });
    expect(qc.getQueryData<Feed>(feedKey)?.items[0]).toMatchObject({ revision: 2, seen: false });
  });

  it("resolves a stale receipt using only selected detail and reconciles the cached summary", async () => {
    const qc = createTestQueryClient();
    qc.setQueryData(feedKey, feedData());
    get.mockResolvedValueOnce({ ...announcement, content: "Old" }).mockResolvedValueOnce({ ...announcement, revision: 2, content: "New" });
    const conflict = new ApiError(409, "Announcement changed");
    post.mockRejectedValue(conflict);
    const { result } = renderHook(() => ({ feed: useAnnouncements(1), detail: useAnnouncementDetail(11, 1), seen: useMarkAnnouncementSeen(1) }), { wrapper: queryClientWrapper(qc) });
    await waitFor(() => expect(result.current.detail.isSuccess).toBe(true));
    await act(async () => { await expect(result.current.seen.mutateAsync({ id: 11, revision: 1 })).rejects.toBe(conflict); });
    await waitFor(() => expect(result.current.detail.data?.revision).toBe(2));
    expect(get.mock.calls).toEqual([["/announcements/11"], ["/announcements/11"]]);
    expect(qc.getQueryData<Feed>(feedKey)?.items[0]).toMatchObject({ revision: 2, seen: false });
    expect(qc.getQueryData<Feed>(feedKey)?.items[0]).not.toHaveProperty("content");
  });

  it("admin create, edit and delete refresh admin summaries but never received or public feeds", async () => {
    const { result } = renderHook(() => ({ admin: useAdminAnnouncements(1, 10, 1), received: useAnnouncements(1), publicFeed: useAnnouncements(), create: useCreateAnnouncement(), update: useUpdateAnnouncement(), remove: useDeleteAnnouncement() }), { wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.admin.isSuccess && result.current.received.isSuccess && result.current.publicFeed.isSuccess).toBe(true));
    // Explicit admin mutations refresh only active admin pages.
    get.mockClear();
    const input = { title: "Title", content: "Body", group_ids: [], show_for_guests: true, pinned: false };
    await act(async () => { await result.current.create.mutateAsync(input); });
    await act(async () => { await result.current.update.mutateAsync({ id: 11, ...input }); });
    await act(async () => { await result.current.remove.mutateAsync(11); });
    expect(get.mock.calls).toEqual(Array.from({ length: 3 }, () => ["/admin/announcements?page=1&page_size=10"]));
  });

  it.each(["create", "update", "remove"] as const)("admin %s clears inactive cached pages before their next visit", async (mutation) => {
    const qc = createTestQueryClient();
    const secondKey = ["announcements", "admin", 1, "feed", 2, 10];
    qc.setQueryData(secondKey, { ...firstPage, page: 2, total: 20 });
    const { result, rerender } = renderHook(({ page }) => ({ feed: useAdminAnnouncements(page, 10, 1), create: useCreateAnnouncement(), update: useUpdateAnnouncement(), remove: useDeleteAnnouncement() }),
      { initialProps: { page: 1 }, wrapper: queryClientWrapper(qc) });
    await waitFor(() => expect(result.current.feed.isSuccess).toBe(true));
    const input = { title: "Title", content: "Body", group_ids: [], show_for_guests: true, pinned: false };
    await act(async () => {
      if (mutation === "create") await result.current.create.mutateAsync(input);
      else if (mutation === "update") await result.current.update.mutateAsync({ id: 11, ...input });
      else await result.current.remove.mutateAsync(11);
    });
    expect(qc.getQueryData(secondKey)).toBeUndefined();
    get.mockResolvedValueOnce({ ...firstPage, page: 2, items: [] });
    rerender({ page: 2 });
    await waitFor(() => expect(result.current.feed.data?.page).toBe(2));
    expect(result.current.feed.data?.items).toEqual([]);
    expect(get).toHaveBeenLastCalledWith("/admin/announcements?page=2&page_size=10");
  });

  it.each([1, undefined])("consumes identity %s hover once without publishing summaries or receipts before opening", async (userId) => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const prefix = userId === undefined ? ["announcements", "public"] : ["announcements", "received", userId];
    const key = [...prefix, "feed", 1, 10];
    const prefetchKey = [...prefix, "prefetch", 11];
    qc.setQueryData(key, feedData());
    const body = { ...announcement, title: "Updated title", revision: 2, seen: true, content: "Authorized body" };
    get.mockResolvedValueOnce(body).mockRejectedValueOnce(new ApiError(403, "Access revoked"));
    const hover = renderHook(() => usePrefetchAnnouncementDetail(userId), { wrapper });
    expect(get).not.toHaveBeenCalled();
    await act(async () => { await hover.result.current(11); });
    expect(qc.getQueryData(key)).toEqual(feedData());
    expect(qc.getQueryData([...prefix, "detail", 11])).toBeUndefined();
    expect(post).not.toHaveBeenCalled();
    const opened = renderHook(() => useAnnouncementDetail(11, userId), { wrapper });
    await waitFor(() => expect(opened.result.current.data?.content).toBe("Authorized body"));
    expect(get).toHaveBeenCalledTimes(1);
    expect(qc.getQueryData(prefetchKey)).toBeUndefined();
    expect(qc.getQueryData<Feed>(key)?.items[0]).toMatchObject({ title: "Updated title", revision: 2, seen: true });
    expect(qc.getQueryData<Feed>(key)?.items[0]).not.toHaveProperty("content");
    opened.unmount();
    await waitFor(() => expect(qc.getQueryData([...prefix, "detail", 11])).toBeUndefined());
    const reopened = renderHook(() => useAnnouncementDetail(11, userId), { wrapper });
    expect(reopened.result.current.data).toBeUndefined();
    await waitFor(() => expect(reopened.result.current.isError).toBe(true));
    expect(reopened.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
    expect(post).not.toHaveBeenCalled();
  });

  it("deduplicates pending hover and open requests without prematurely publishing a body", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const { promise, resolve } = createDeferred<typeof announcement & { content: string }>();
    get.mockReturnValueOnce(promise);
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    let first!: Promise<void>;
    let repeated!: Promise<void>;
    act(() => { first = hover.result.current(11); repeated = hover.result.current(11); });
    const opened = renderHook(() => useAnnouncementDetail(11, 1), { wrapper });
    expect(opened.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(1);
    expect(post).not.toHaveBeenCalled();
    await act(async () => { resolve({ ...announcement, content: "Pending body" }); await Promise.all([first, repeated]); });
    await waitFor(() => expect(opened.result.current.data?.content).toBe("Pending body"));
    expect(get).toHaveBeenCalledTimes(1);
    expect(qc.getQueryData(["announcements", "received", 1, "prefetch", 11])).toBeUndefined();
  });

  it("claims a pending hover only once even when the first open closes before completion", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const { promise, resolve } = createDeferred<typeof announcement & { content: string }>();
    get.mockReturnValueOnce(promise).mockResolvedValueOnce({ ...announcement, content: "Reauthorized body" });
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    let prefetch!: Promise<void>;
    act(() => { prefetch = hover.result.current(11); });
    const first = renderHook(() => useAnnouncementDetail(11, 1), { wrapper });
    expect(first.result.current.data).toBeUndefined();
    first.unmount();
    await waitFor(() => expect(qc.getQueryCache().find({ queryKey: ["announcements", "received", 1, "detail", 11], exact: true })).toBeUndefined());
    const reopened = renderHook(() => useAnnouncementDetail(11, 1), { wrapper });
    await waitFor(() => expect(reopened.result.current.data?.content).toBe("Reauthorized body"));
    await act(async () => { resolve({ ...announcement, content: "First hover body" }); await prefetch; });
    expect(reopened.result.current.data?.content).toBe("Reauthorized body");
    expect(get).toHaveBeenCalledTimes(2);
    expect(post).not.toHaveBeenCalled();
  });

  it("expires completed hover authorization even before its garbage-collection timer runs", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    get.mockResolvedValueOnce({ ...announcement, content: "Expired body" }).mockRejectedValueOnce(new ApiError(403, "Revoked"));
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    await act(async () => { await hover.result.current(11); });
    const clock = vi.spyOn(Date, "now").mockReturnValue(Date.now() + 5_001);
    try {
      const opened = renderHook(() => useAnnouncementDetail(11, 1), { wrapper });
      expect(opened.result.current.data).toBeUndefined();
      await waitFor(() => expect(opened.result.current.isError).toBe(true));
      expect(opened.result.current.data).toBeUndefined();
      expect(get).toHaveBeenCalledTimes(2);
      expect(post).not.toHaveBeenCalled();
    } finally {
      clock.mockRestore();
    }
  });

  it("does not reuse a pending hover after its authorization window expires", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const { promise, resolve } = createDeferred<typeof announcement & { content: string }>();
    get.mockReturnValueOnce(promise)
      .mockResolvedValueOnce({ ...announcement, content: "Freshly authorized" });
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    let prefetch!: Promise<void>;
    act(() => { prefetch = hover.result.current(11); });
    const clock = vi.spyOn(Date, "now").mockReturnValue(Date.now() + 5_001);
    try {
      const opened = renderHook(() => useAnnouncementDetail(11, 1), { wrapper });
      expect(opened.result.current.data).toBeUndefined();
      await waitFor(() => expect(opened.result.current.data?.content).toBe("Freshly authorized"));
      await act(async () => { resolve({ ...announcement, content: "Expired body" }); await prefetch; });
      expect(opened.result.current.data?.content).toBe("Freshly authorized");
      expect(get).toHaveBeenCalledTimes(2);
    } finally {
      clock.mockRestore();
    }
  });

  it.each([false, true])("falls back to an authorized detail request when pending=%s prefetch fails", async (pending) => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const { promise, reject } = createDeferred<typeof announcement & { content: string }>();
    get.mockReturnValueOnce(promise)
      .mockRejectedValueOnce(new ApiError(403, "Still unauthorized"));
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    let prefetch!: Promise<void>;
    act(() => { prefetch = hover.result.current(11); });
    if (!pending) await act(async () => { reject(new Error("Hover failed")); await prefetch; });
    const opened = renderHook(() => {
      const { data, isError } = useAnnouncementDetail(11, 1);
      return { data, isError };
    }, { wrapper });
    expect(opened.result.current.data).toBeUndefined();
    if (pending) await act(async () => { reject(new Error("Hover failed")); await prefetch; });
    await waitFor(() => expect(opened.result.current.isError).toBe(true));
    expect(opened.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
    expect(post).not.toHaveBeenCalled();
    expect(qc.getQueryData(feedKey)).toBeUndefined();
    expect(qc.getQueryData(["announcements", "received", 1, "prefetch", 11])).toBeUndefined();
  });

  it.each([2, undefined])("never consumes a private hover under identity %s", async (nextUserId) => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    get.mockResolvedValueOnce({ ...announcement, show_for_guests: false, content: "Private body" })
      .mockRejectedValueOnce(new ApiError(403, "Not authorized"));
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    await act(async () => { await hover.result.current(11); });
    const opened = renderHook(() => useAnnouncementDetail(11, nextUserId), { wrapper });
    expect(opened.result.current.data).toBeUndefined();
    await waitFor(() => expect(opened.result.current.isError).toBe(true));
    expect(opened.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
    expect(post).not.toHaveBeenCalled();
  });

  it("keeps late hover responses isolated after switching identities", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const { promise, resolve } = createDeferred<typeof announcement & { content: string }>();
    get.mockReturnValueOnce(promise)
      .mockResolvedValueOnce({ ...announcement, content: "Second user's body" });
    const hover = renderHook(({ userId }) => usePrefetchAnnouncementDetail(userId), { initialProps: { userId: 1 }, wrapper });
    let oldPrefetch!: Promise<void>;
    act(() => { oldPrefetch = hover.result.current(11); });
    hover.rerender({ userId: 2 });
    await act(async () => { await hover.result.current(11); });
    const opened = renderHook(() => useAnnouncementDetail(11, 2), { wrapper });
    await waitFor(() => expect(opened.result.current.data?.content).toBe("Second user's body"));
    await act(async () => { resolve({ ...announcement, show_for_guests: false, content: "First user's private body" }); await oldPrefetch; });
    expect(opened.result.current.data?.content).toBe("Second user's body");
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("does not use recipient hover authorization for admin editing", async () => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    get.mockResolvedValueOnce({ ...announcement, content: "Recipient body" })
      .mockResolvedValueOnce({ ...announcement, content: "Admin body" });
    const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper });
    await act(async () => { await hover.result.current(11); });
    const opened = renderHook(() => useAnnouncementDetail(11, 1, true), { wrapper });
    await waitFor(() => expect(opened.result.current.data?.content).toBe("Admin body"));
    expect(get.mock.calls).toEqual([["/announcements/11"], ["/admin/announcements/11"]]);
  });

  it("garbage collects unused hover bodies after five seconds", async () => {
    const qc = createTestQueryClient();
    get.mockResolvedValueOnce({ ...announcement, content: "Unused body" });
    vi.useFakeTimers();
    try {
      const hover = renderHook(() => usePrefetchAnnouncementDetail(1), { wrapper: queryClientWrapper(qc) });
      await act(async () => { await hover.result.current(11); });
      const key = ["announcements", "received", 1, "prefetch", 11];
      expect(qc.getQueryData(key)).toBeDefined();
      await act(async () => { await vi.advanceTimersByTimeAsync(5_001); });
      expect(qc.getQueryData(key)).toBeUndefined();
      expect(get).toHaveBeenCalledTimes(1);
      expect(post).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it.each(["admin", "received", "public"] as const)("reauthorizes %s detail on every open without retaining a body after access is revoked", async (scope) => {
    const qc = createTestQueryClient();
    const wrapper = queryClientWrapper(qc);
    const useDetail = () => useAnnouncementDetail(11, scope === "public" ? undefined : 1, scope === "admin");
    get.mockResolvedValueOnce({ ...announcement, content: "Previously authorized body" });
    const first = renderHook(useDetail, { wrapper });
    await waitFor(() => expect(first.result.current.data?.content).toBe("Previously authorized body"));
    first.unmount();
    await waitFor(() => expect(qc.getQueryCache().findAll({ predicate: ({ queryKey }) => queryKey.includes("detail") })).toHaveLength(0));
    get.mockRejectedValueOnce(new ApiError(403, "Access revoked"));
    const reopened = renderHook(useDetail, { wrapper });
    expect(reopened.result.current.data).toBeUndefined();
    await waitFor(() => expect(reopened.result.current.isError).toBe(true));
    expect(reopened.result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each([false, true])("does not reuse another user's detail authorization when admin is %s", async (admin) => {
    get.mockResolvedValueOnce({ ...announcement, content: "First user's private body" })
      .mockRejectedValueOnce(new ApiError(403, "Access revoked"));
    const { result, rerender } = renderHook(({ userId }) => useAnnouncementDetail(11, userId, admin), { initialProps: { userId: 1 }, wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.data?.content).toBe("First user's private body"));
    rerender({ userId: 2 });
    expect(result.current.data).toBeUndefined();
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each(["admin", "received", "public"] as const)("uses the %s detail endpoint and exposes access errors without automatic retry", async (scope) => {
    get.mockRejectedValue(new ApiError(404, "Not visible"));
    const { result } = renderHook(() => useAnnouncementDetail(11, scope === "public" ? undefined : 1, scope === "admin"), { wrapper: queryClientWrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith(scope === "admin" ? "/admin/announcements/11" : "/announcements/11");
    expect(result.current.data).toBeUndefined();
  });
});
