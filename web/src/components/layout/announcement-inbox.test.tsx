import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Announcement, ReceivedAnnouncementSummary } from "@/lib/api/announcements";
import { AnnouncementInbox } from "./announcement-inbox";

const mocks = vi.hoisted(() => ({
  auth: { user: null as null | { user_id: number }, loading: false },
  feed: [] as ReceivedAnnouncementSummary[],
  extraPages: [] as ReceivedAnnouncementSummary[][],
  detailQuery: vi.fn(),
  detailRefetch: vi.fn(),
  detailLoading: false,
  detailError: false,
  details: {} as Record<number, Announcement>,
  retainedDetail: undefined as Announcement | undefined,
  bodies: {} as Record<number, string>,
  mark: vi.fn(),
  prefetch: vi.fn(),
  prefetchPage: vi.fn(),
  publicQuery: vi.fn(),
  refetch: vi.fn(),
  fetched: true,
  loading: false,
  error: false,
}));

vi.mock("@/lib/auth", () => ({ useAuth: () => mocks.auth }));
vi.mock("next-intl", () => ({
  useLocale: () => "zh",
  useTranslations: () => (key: string, values?: { count?: number; page?: number }) =>
    values ? `${key}:${values.count ?? values.page}` : key,
}));
vi.mock("@/lib/api/announcements", () => ({
  useAnnouncements: (...args: [number?, boolean?, number?]) => {
    const [userId, , page = 1] = args;
    if (userId === undefined) mocks.publicQuery(true);
    const pages = [mocks.feed, ...mocks.extraPages];
    const total = pages.reduce((sum, items) => sum + items.length, 0);
    return { data: mocks.fetched ? { items: pages[page - 1] ?? [], total, page, page_size: 10 } : undefined, total, loadedItems: mocks.fetched ? pages.flat() : [], isLoading: mocks.loading, isFetching: mocks.loading, isFetched: mocks.fetched, isError: mocks.error, refetch: mocks.refetch };
  },
  useAnnouncementDetail: (id?: number, userId?: number) => {
    mocks.detailQuery(id, userId);
    const item = [...mocks.feed, ...mocks.extraPages.flat()].find((entry) => entry.id === id);
    return { data: mocks.retainedDetail ?? (item && !mocks.detailLoading && !mocks.detailError ? mocks.details[item.id] ?? { ...item, content: mocks.bodies[item.id] ?? `<script>announcement ${item.id}</script>` } : undefined), isLoading: mocks.detailLoading, isFetching: mocks.detailLoading, isError: mocks.detailError, refetch: mocks.detailRefetch };
  },
  usePrefetchAnnouncementDetail: () => mocks.prefetch,
  usePrefetchAnnouncementPage: () => mocks.prefetchPage,
  useMarkAnnouncementSeen: () => ({ mutateAsync: mocks.mark, isPending: false }),
}));

function announcement(id: number): ReceivedAnnouncementSummary {
  return { id, title: `Announcement ${id}`, group_ids: [], show_for_guests: true, pinned: false, revision: 1, created_at: 1, updated_at: 1, seen: false };
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.detailRefetch.mockReset();
  mocks.refetch.mockReset();
  mocks.prefetch.mockReset().mockResolvedValue(undefined);
  mocks.prefetchPage.mockReset().mockResolvedValue(undefined);
  localStorage.clear();
  mocks.auth = { user: null, loading: false };
  mocks.feed = [];
  mocks.fetched = true;
  mocks.loading = false;
  mocks.error = false;
  mocks.extraPages = [];
  mocks.detailLoading = false;
  mocks.detailError = false;
  mocks.details = {};
  mocks.retainedDetail = undefined;
  mocks.bodies = {};
  mocks.mark.mockResolvedValue({ status: "ok" });
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("AnnouncementInbox", () => {
  it("does not acknowledge a summary until successful visible detail arrives, using its actual revision", async () => {
    mocks.auth.user = { user_id: 2301 };
    mocks.feed = [announcement(2301)];
    mocks.detailLoading = true;
    const view = render(<AnnouncementInbox />);
    expect(await screen.findByRole("status")).toHaveTextContent("loading");
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    mocks.details[2301] = { ...announcement(2301), revision: 2, content: "Actual detail" };
    view.rerender(<AnnouncementInbox />);
    await screen.findByText("Actual detail");
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 2301, revision: 2 }));
    expect(mocks.mark).not.toHaveBeenCalledWith({ id: 2301, revision: 1 });
    expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("2301:2");
    expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).not.toContain("2301:1");
  });

  it.each([true, false])("offers retry for detail access failure without recording a receipt (guest: %s)", async (guest) => {
    mocks.auth.user = guest ? null : { user_id: 2401 };
    mocks.feed = [announcement(2401)];
    mocks.detailError = true;
    render(<AnnouncementInbox guestsOnly={guest} />);
    expect(screen.getByRole("alert")).toHaveTextContent("detailError");
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(mocks.detailRefetch).toHaveBeenCalledTimes(1);
    expect(mocks.mark).not.toHaveBeenCalled();
    expect(localStorage.getItem("announcement-seen-ids")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^close$/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("hides retained bodies during authorization and after permission errors, including when another item opens", async () => {
    mocks.auth.user = { user_id: 2451 };
    mocks.feed = [announcement(2451), announcement(2452)];
    mocks.detailLoading = true;
    mocks.retainedDetail = { ...announcement(2451), content: "Previously authorized body" };
    const view = render(<AnnouncementInbox />);
    expect(await screen.findByRole("status")).toHaveTextContent("loading");
    expect(screen.queryByText("Previously authorized body")).not.toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    mocks.detailError = true;
    view.rerender(<AnnouncementInbox />);
    expect(screen.getByRole("alert")).toHaveTextContent("detailError");
    expect(screen.queryByText("Previously authorized body")).not.toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: "next" }));
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(screen.getByRole("heading", { name: "Announcement 2452" })).toBeInTheDocument();
    mocks.detailError = false;
    view.rerender(<AnnouncementInbox />);
    expect(screen.queryByText("Previously authorized body")).not.toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.retainedDetail = undefined;
    view.rerender(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 2452, revision: 1 }));
    fireEvent.click(screen.getByRole("button", { name: "back" }));
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(screen.getByRole("button", { name: /Announcement 2451/ })).toBeInTheDocument();
  });

  it("shows accessible unread dots on the bell and list item until visible detail is read", async () => {
    mocks.auth.user = { user_id: 2461 };
    const view = render(<AnnouncementInbox />);
    const bell = screen.getByRole("button", { name: "open" });
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    mocks.feed = [announcement(2461)];
    mocks.detailLoading = true;
    view.rerender(<AnnouncementInbox />);
    expect(bell).toHaveAccessibleName("unreadCount:1");
    expect(bell.textContent).toBe("");
    expect(bell.querySelector("span[aria-hidden='true']")).not.toBeNull();
    const item = screen.getByRole("button", { name: /Announcement 2461.*unread/ });
    expect(within(item).getByText("unread")).toBeInTheDocument();
    expect(item.querySelector("span[aria-hidden='true']")).not.toBeNull();
    fireEvent.click(item);
    expect(bell).toHaveAccessibleName("unreadCount:1");
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    view.rerender(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 2461, revision: 1 }));
    expect(bell).toHaveAccessibleName("open");
    expect(bell.querySelector("span[aria-hidden='true']")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "back" }));
    const readItem = screen.getByRole("button", { name: /Announcement 2461/ });
    expect(within(readItem).queryByText("unread")).not.toBeInTheDocument();
    expect(readItem.querySelector("span[aria-hidden='true']")).toBeNull();
  });

  it("clears the prior identity's dialog while loading the new identity without exposing retained details", async () => {
    mocks.auth.user = { user_id: 2471 };
    mocks.feed = [announcement(2471)];
    mocks.details[2471] = { ...announcement(2471), content: "First user's body" };
    const view = render(<AnnouncementInbox />);
    await screen.findByText("First user's body");
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 2471, revision: 1 }));
    mocks.auth = { user: { user_id: 2472 }, loading: false };
    mocks.fetched = false;
    mocks.feed = [announcement(2472)];
    mocks.retainedDetail = mocks.details[2471];
    mocks.detailLoading = true;
    view.rerender(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByText("First user's body")).not.toBeInTheDocument();
    mocks.fetched = true;
    view.rerender(<AnnouncementInbox />);
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("heading", { name: "Announcement 2472" })).toBeInTheDocument();
    expect(await screen.findByRole("status")).toHaveTextContent("loading");
    expect(screen.queryByText("First user's body")).not.toBeInTheDocument();
    mocks.detailLoading = false;
    mocks.detailError = true;
    view.rerender(<AnnouncementInbox />);
    expect(screen.getByRole("alert")).toHaveTextContent("detailError");
    expect(screen.queryByText("First user's body")).not.toBeInTheDocument();
    expect(mocks.mark).toHaveBeenCalledTimes(1);
  });

  it("loads older pages only on explicit action and allows previous/next without retaining all rows in the DOM", async () => {
    mocks.auth.user = { user_id: 2501 };
    mocks.feed = Array.from({ length: 10 }, (_, index) => ({ ...announcement(25010 + index), seen: true }));
    const older = Array.from({ length: 10 }, (_, index) => ({ ...announcement(25020 + index), seen: true }));
    mocks.extraPages = [older];
    render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(25010, 2501);
    expect(screen.getAllByRole("listitem")).toHaveLength(10);
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    await screen.findByRole("button", { name: /Announcement 25020/ });
    expect(screen.getAllByRole("listitem")).toHaveLength(10);
    expect(screen.queryByRole("button", { name: /Announcement 25010/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "previousPage" }));
    expect(screen.getByRole("button", { name: /Announcement 25010/ })).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(10);
    expect(screen.queryByRole("button", { name: /Announcement 25020/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    expect(screen.getByRole("button", { name: /Announcement 25020/ })).toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
  });

  it("keeps popup history when the same user logs in again without a full page load", async () => {
    mocks.auth = { user: { user_id: 2601 }, loading: false };
    mocks.feed = [announcement(2601)];
    const view = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledTimes(1));
    mocks.auth = { user: null, loading: false };
    view.rerender(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    mocks.auth = { user: { user_id: 2601 }, loading: false };
    view.rerender(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(mocks.refetch).not.toHaveBeenCalled();
  });


  it("waits for auth hydration and never loads a guest feed for authenticated visitors", () => {
    mocks.auth.loading = true;
    const view = render(<AnnouncementInbox guestsOnly />);
    expect(mocks.publicQuery).not.toHaveBeenCalled();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    mocks.auth = { user: { user_id: 10 }, loading: false };
    view.rerender(<AnnouncementInbox guestsOnly />);
    expect(mocks.publicQuery).not.toHaveBeenCalled();
  });

  it("shows unseen announcements sequentially and marks only the displayed detail", async () => {
    mocks.auth.user = { user_id: 11 };
    mocks.feed = [announcement(1101), announcement(1102)];
    render(<AnnouncementInbox />);
    await screen.findByRole("heading", { name: "Announcement 1101" });
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1101, revision: 1 }));
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(screen.getByText("<script>announcement 1101</script>").querySelector("script")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "next" }));
    await screen.findByRole("heading", { name: "Announcement 1102" });
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1102, revision: 1 }));
    fireEvent.click(screen.getByRole("button", { name: /^close$/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("does not mark unseen items merely by opening the bell list", async () => {
    mocks.auth.user = { user_id: 12 };
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    mocks.feed = [announcement(1201)];
    view.rerender(<AnnouncementInbox />);
    expect(mocks.mark).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /Announcement 1201/ }));
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1201, revision: 1 }));
  });

  it("preserves guest receipts and suppresses repeat popups across route remounts", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify(["1301:1"]));
    mocks.feed = [announcement(1301), announcement(1302)];
    const first = render(<AnnouncementInbox guestsOnly />);
    await screen.findByRole("heading", { name: "Announcement 1302" });
    await waitFor(() => expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("1302:1"));
    expect(mocks.mark).not.toHaveBeenCalled();
    first.unmount();
    render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not loop after persistence fails, including on route remount", async () => {
    mocks.auth.user = { user_id: 14 };
    mocks.feed = [announcement(1401)];
    mocks.mark.mockRejectedValue(new Error("offline"));
    const first = render(<AnnouncementInbox />);
    await screen.findByText("seenError");
    first.unmount();
    render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.mark).toHaveBeenCalledTimes(1);
  });

  it("waits until the tab is visible before showing or marking an automatic popup", async () => {
    mocks.auth.user = { user_id: 15 };
    mocks.feed = [announcement(1501)];
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    act(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1501, revision: 1 }));
  });

  it("offers loading, empty, and retry states in the list", async () => {
    mocks.auth.user = { user_id: 16 };
    mocks.loading = true;
    mocks.fetched = false;
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    expect(await screen.findByRole("status")).toHaveTextContent("loading");
    mocks.loading = false;
    mocks.fetched = true;
    view.rerender(<AnnouncementInbox />);
    expect(screen.getByText("empty")).toBeInTheDocument();
    mocks.error = true;
    view.rerender(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(mocks.refetch).toHaveBeenCalledTimes(1);
  });

  it("opens newly fetched IDs but does not re-alert when an announcement is edited", async () => {
    mocks.auth.user = { user_id: 17 };
    mocks.feed = [announcement(1701)];
    const view = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1701, revision: 1 }));
    fireEvent.click(screen.getByRole("button", { name: /^close$/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    mocks.bodies[1701] = "Edited body";
    mocks.feed = [{ ...announcement(1701), updated_at: 2 }];
    view.rerender(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    mocks.feed = [announcement(1702), ...mocks.feed];
    view.rerender(<AnnouncementInbox />);
    await screen.findByRole("heading", { name: "Announcement 1702" });
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1702, revision: 1 }));
  });

  it("shows creation dates with the year only for previous years and preserves feed order", () => {
    mocks.auth.user = { user_id: 18 };
    const year = new Date().getFullYear();
    const current = new Date(year, 4, 9, 12);
    const previous = new Date(year - 1, 10, 3, 12);
    mocks.feed = [
      { ...announcement(1801), pinned: true, created_at: previous.getTime() / 1000, seen: true },
      { ...announcement(1802), created_at: current.getTime() / 1000, seen: true },
    ];
    render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    const items = screen.getAllByRole("listitem");
    expect(items[0]).toHaveTextContent("Announcement 1801");
    expect(items[0]).toHaveTextContent("pinned");
    expect(items[1]).toHaveTextContent("Announcement 1802");
    expect(items[1]).not.toHaveTextContent("pinned");
    expect(screen.getByText(`${year - 1}年11月3日`)).toHaveAttribute("dateTime", previous.toISOString());
    expect(screen.getByText("5月9日")).toHaveAttribute("dateTime", current.toISOString());
  });

  it("reopens an acknowledged ID only when its revision changes", async () => {
    mocks.auth.user = { user_id: 19 };
    mocks.feed = [announcement(1901)];
    const view = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1901, revision: 1 }));
    fireEvent.click(screen.getByRole("button", { name: /^close$/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    mocks.bodies[1901] = "Reannounced body";
    mocks.feed = [{ ...announcement(1901), revision: 2 }];
    view.rerender(<AnnouncementInbox />);
    await screen.findByText("Reannounced body");
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 1901, revision: 2 }));
    expect(mocks.mark).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole("button", { name: /^close$/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    view.unmount();
    render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("replaces malformed guest receipts after displaying a detail", async () => {
    localStorage.setItem("announcement-seen-ids", "{broken");
    mocks.feed = [announcement(2901)];
    const view = render(<AnnouncementInbox guestsOnly />);
    await screen.findByRole("heading", { name: "Announcement 2901" });
    await waitFor(() => expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("2901:1"));
    expect(screen.queryByText("seenError")).not.toBeInTheDocument();
    view.unmount();
    render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps write failures visible and retries when storage recovers", async () => {
    mocks.feed = [announcement(2902)];
    const write = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    try {
      render(<AnnouncementInbox guestsOnly />);
      await screen.findByText("seenError");
      expect(localStorage.getItem("announcement-seen-ids")).toBeNull();
    } finally {
      write.mockRestore();
    }
    fireEvent.click(screen.getByRole("button", { name: "retrySeen" }));
    await waitFor(() => expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("2902:1"));
    expect(screen.queryByText("seenError")).not.toBeInTheDocument();
  });

  it.each([true, false])("isolates a stale receipt completion when the new receipt succeeds: %s", async (newSucceeded) => {
    const id = newSucceeded ? 2101 : 2201;
    mocks.auth.user = { user_id: id };
    let resolveOld!: (value: { status: string }) => void;
    let rejectOld!: (reason: Error) => void;
    const oldReceipt = new Promise<{ status: string }>((resolve, reject) => {
      resolveOld = resolve;
      rejectOld = reject;
    });
    mocks.mark.mockImplementation(({ revision }: { revision: number }) => revision === 1
      ? oldReceipt
      : newSucceeded ? Promise.resolve({ status: "ok" }) : Promise.reject(new Error("offline")));
    mocks.feed = [announcement(id)];
    const view = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id, revision: 1 }));
    // The same detail stays open while a newer revision arrives.
    mocks.bodies[id] = "Updated while open";
    mocks.feed = [{ ...announcement(id), revision: 2 }];
    view.rerender(<AnnouncementInbox />);
    await screen.findByText("Updated while open");
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id, revision: 2 }));
    if (!newSucceeded) await screen.findByRole("alert");
    await act(async () => {
      if (newSucceeded) rejectOld(new Error("old revision rejected"));
      else resolveOld({ status: "ok" });
    });
    if (newSucceeded) expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    else expect(screen.getByRole("alert")).toHaveTextContent("seenError");
  });

  it.each(["hover", "focus"] as const)("prefetches after a 150ms %s dwell without reading or acknowledging the item", async (interaction) => {
    vi.useFakeTimers();
    const id = interaction === "hover" ? 3101 : 3102;
    mocks.auth.user = { user_id: id };
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    mocks.feed = [announcement(id)];
    view.rerender(<AnnouncementInbox />);
    const item = screen.getByRole("button", { name: new RegExp(`Announcement ${id}`) });
    if (interaction === "hover") fireEvent.mouseEnter(item);
    else fireEvent.focus(item);
    await act(async () => { vi.advanceTimersByTime(149); });
    expect(mocks.prefetch).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(mocks.prefetch).toHaveBeenCalledTimes(1);
    expect(mocks.prefetch).toHaveBeenCalledWith(id);
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(id, id);
    expect(mocks.mark).not.toHaveBeenCalled();
    expect(localStorage.getItem("announcement-seen-ids")).toBeNull();
    expect(within(item).getByText("unread")).toBeInTheDocument();
    expect(item).toBeInTheDocument();
  });

  it.each(["hover", "focus"] as const)("cancels a quick %s pass and starts a new full dwell on return", async (interaction) => {
    vi.useFakeTimers();
    const id = interaction === "hover" ? 3111 : 3112;
    mocks.auth.user = { user_id: id };
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    mocks.feed = [announcement(id)];
    view.rerender(<AnnouncementInbox />);
    const item = screen.getByRole("button", { name: new RegExp(`Announcement ${id}`) });
    if (interaction === "hover") fireEvent.mouseEnter(item);
    else fireEvent.focus(item);
    await act(async () => { vi.advanceTimersByTime(100); });
    if (interaction === "hover") fireEvent.mouseLeave(item);
    else fireEvent.blur(item);
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(mocks.prefetch).not.toHaveBeenCalled();
    if (interaction === "hover") fireEvent.mouseEnter(item);
    else fireEvent.focus(item);
    await act(async () => { vi.advanceTimersByTime(149); });
    expect(mocks.prefetch).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(mocks.prefetch).toHaveBeenCalledTimes(1);
    expect(mocks.prefetch).toHaveBeenCalledWith(id);
    expect(mocks.mark).not.toHaveBeenCalled();
  });

  it.each(["unmount", "page"] as const)("cancels pending prefetch on %s changes", async (change) => {
    vi.useFakeTimers();
    const id = change === "unmount" ? 3121 : 3122;
    mocks.auth.user = { user_id: id };
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    mocks.feed = Array.from({ length: 10 }, (_, index) => announcement(id * 10 + index));
    mocks.extraPages = [[announcement(id * 10 + 10)]];
    view.rerender(<AnnouncementInbox />);
    fireEvent.mouseEnter(screen.getByRole("button", { name: new RegExp(`Announcement ${id * 10}`) }));
    await act(async () => { vi.advanceTimersByTime(100); });
    if (change === "unmount") view.unmount();
    else fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(mocks.prefetch).not.toHaveBeenCalled();
    expect(mocks.mark).not.toHaveBeenCalled();
  });

  it("imports only exact public browser receipts on login without fetching a body or repeating sync on remount", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify(["3201:1"]));
    mocks.feed = [announcement(3201)];
    const guest = render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    guest.unmount();
    mocks.auth.user = { user_id: 3201 };
    const account = render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 3201, revision: 1 }));
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(3201, 3201);
    expect(mocks.prefetch).not.toHaveBeenCalled();
    account.unmount();
    render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.mark).toHaveBeenCalledTimes(1);
  });

  it("mirrors server-seen public summaries for guest remounts without loading their bodies", async () => {
    mocks.auth.user = { user_id: 3211 };
    mocks.feed = [{ ...announcement(3211), seen: true }];
    const account = render(<AnnouncementInbox />);
    await waitFor(() => expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("3211:1"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(3211, 3211);
    expect(mocks.mark).not.toHaveBeenCalled();
    account.unmount();
    mocks.auth.user = null;
    mocks.feed = [announcement(3211)];
    render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "open" })).toBeInTheDocument();
  });

  it("does not export private summaries or displayed private details to browser receipts", async () => {
    mocks.auth.user = { user_id: 3221 };
    mocks.feed = [
      { ...announcement(3221), show_for_guests: false, seen: true },
      { ...announcement(3222), show_for_guests: false },
    ];
    render(<AnnouncementInbox />);
    await screen.findByRole("heading", { name: "Announcement 3222" });
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 3222, revision: 1 }));
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem("announcement-seen-ids")).toBeNull();
  });

  it("does not let an old public browser receipt suppress a currently private announcement", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify(["3231:1"]));
    mocks.auth.user = { user_id: 3231 };
    mocks.feed = [{ ...announcement(3231), show_for_guests: false }];
    mocks.detailLoading = true;
    const view = render(<AnnouncementInbox />);
    expect(screen.getByRole("heading", { name: "Announcement 3231" })).toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    view.rerender(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 3231, revision: 1 }));
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toEqual(["3231:1"]);
  });

  it("still alerts for a public revision newer than its browser receipt", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify(["3241:1"]));
    mocks.auth.user = { user_id: 3241 };
    mocks.feed = [{ ...announcement(3241), revision: 2 }];
    mocks.detailLoading = true;
    const view = render(<AnnouncementInbox />);
    expect(screen.getByRole("heading", { name: "Announcement 3241" })).toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    view.rerender(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id: 3241, revision: 2 }));
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toContain("3241:2");
  });

  it("keeps browser receipt sync failures retryable without popup or automatic retry loops", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify(["3251:1"]));
    mocks.auth.user = { user_id: 3251 };
    mocks.feed = [announcement(3251)];
    mocks.mark.mockRejectedValue(new Error("offline"));
    const first = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    first.rerender(<AnnouncementInbox />);
    first.unmount();
    render(<AnnouncementInbox />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    await screen.findByText("seenError");
    expect(mocks.mark).toHaveBeenCalledTimes(1);
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(3251, 3251);
    mocks.mark.mockResolvedValue({ status: "ok" });
    fireEvent.click(screen.getByRole("button", { name: "retrySeen" }));
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledTimes(2));
    expect(mocks.mark).toHaveBeenLastCalledWith({ id: 3251, revision: 1 });
    await waitFor(() => expect(screen.queryByText("seenError")).not.toBeInTheDocument());
    expect(mocks.detailQuery).not.toHaveBeenCalledWith(3251, 3251);
  });

  it("shows fast detail results without flashing or later resurrecting the loading indicator", async () => {
    vi.useFakeTimers();
    mocks.auth.user = { user_id: 3261 };
    mocks.feed = [announcement(3261)];
    mocks.detailLoading = true;
    const view = render(<AnnouncementInbox />);
    await act(async () => { vi.advanceTimersByTime(100); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(mocks.mark).not.toHaveBeenCalled();
    mocks.detailLoading = false;
    view.rerender(<AnnouncementInbox />);
    await act(async () => { vi.advanceTimersByTime(200); });
    expect(screen.getByText("<script>announcement 3261</script>")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
