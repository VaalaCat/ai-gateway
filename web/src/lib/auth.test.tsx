import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { queryClientWrapper } from "@/test/render";
import { useAnnouncements } from "@/lib/api/announcements";
import { STORAGE_KEYS } from "@/lib/constants";
import { useAuth } from "./auth";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("@/lib/api/client", () => ({ api: { get, post }, ApiError: class extends Error {} }));

const token = `header.${btoa(JSON.stringify({ user_id: 77, username: "reader", role: 1, exp: 4_000_000_000 }))}.signature`;
const renewedToken = `header.${btoa(JSON.stringify({ user_id: 77, username: "reader", role: 1, exp: 4_000_000_001 }))}.signature`;

function usePageAnnouncements() {
  const auth = useAuth();
  const feed = useAnnouncements(auth.user?.user_id, !auth.loading);
  return { auth, feed };
}

describe("announcement page-load lifecycle with authentication", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 10 });
    post.mockReset().mockResolvedValue({ token });
  });

  it("loads guest announcements, then automatically loads user announcements after login", async () => {
    const privatePage = { items: [{ id: 22, title: "Reader's private news" }], total: 1, page: 1, page_size: 10 };
    let finish!: (value: typeof privatePage) => void;
    get.mockResolvedValueOnce({ items: [{ id: 11, title: "Public news" }], total: 1, page: 1, page_size: 10 })
      .mockReturnValueOnce(new Promise<typeof privatePage>((resolve) => { finish = resolve; }));
    const wrapper = queryClientWrapper();
    const first = renderHook(usePageAnnouncements, { wrapper });
    await waitFor(() => expect(first.result.current.feed.data?.items[0].title).toBe("Public news"));
    await act(async () => { await first.result.current.auth.login("reader", "password"); });
    expect(first.result.current.auth.user?.user_id).toBe(77);
    expect(first.result.current.feed.data).toBeUndefined();
    expect(first.result.current.feed.loadedItems).toEqual([]);
    expect(get).toHaveBeenCalledTimes(2);
    await act(async () => { finish(privatePage); });
    await waitFor(() => expect(first.result.current.feed.data?.items[0].title).toBe("Reader's private news"));
    first.unmount();
    const second = renderHook(usePageAnnouncements, { wrapper });
    expect(second.result.current.feed.data?.items[0].title).toBe("Reader's private news");
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("loads an authenticated page once without refetching for a token renewal", async () => {
    localStorage.setItem(STORAGE_KEYS.TOKEN, token);
    get.mockResolvedValueOnce({ items: [{ id: 11, title: "Reader's private news" }], total: 1, page: 1, page_size: 10 });
    const wrapper = queryClientWrapper();
    const first = renderHook(usePageAnnouncements, { wrapper });
    await waitFor(() => expect(first.result.current.feed.data?.items[0].title).toBe("Reader's private news"));
    act(() => {
      localStorage.setItem(STORAGE_KEYS.TOKEN, renewedToken);
      window.dispatchEvent(new StorageEvent("storage", { key: STORAGE_KEYS.TOKEN }));
    });
    expect(first.result.current.auth.user?.user_id).toBe(77);
    first.unmount();
    const second = renderHook(usePageAnnouncements, { wrapper });
    expect(second.result.current.feed.data?.items[0].title).toBe("Reader's private news");
    expect(get).toHaveBeenCalledTimes(1);
  });

});
