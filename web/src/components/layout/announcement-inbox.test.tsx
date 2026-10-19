import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReceivedAnnouncement } from "@/lib/api/announcements";
import { AnnouncementInbox } from "./announcement-inbox";

const mocks = vi.hoisted(() => ({
  auth: { user: null as null | { user_id: number }, loading: false },
  feed: [] as ReceivedAnnouncement[],
  mark: vi.fn(),
  publicQuery: vi.fn(),
  refetch: vi.fn(),
  loading: false,
  error: false,
}));

vi.mock("@/lib/auth", () => ({ useAuth: () => mocks.auth }));
vi.mock("next-intl", () => ({
  useLocale: () => "zh",
  useTranslations: () => (key: string, values?: { count: number }) =>
    values ? `${key}:${values.count}` : key,
}));
vi.mock("@/lib/api/announcements", () => ({
  useAnnouncements: () => ({ data: mocks.feed, isLoading: mocks.loading, isError: mocks.error, refetch: mocks.refetch }),
  usePublicAnnouncements: (enabled: boolean) => {
    mocks.publicQuery(enabled);
    return { data: mocks.feed, isLoading: mocks.loading, isError: mocks.error, refetch: mocks.refetch };
  },
  useMarkAnnouncementSeen: () => ({ mutateAsync: mocks.mark, isPending: false }),
}));

function announcement(id: number): ReceivedAnnouncement {
  return { id, title: `Announcement ${id}`, content: `<script>announcement ${id}</script>`, group_ids: [], show_for_guests: true, pinned: false, revision: 1, created_at: 1, updated_at: 1, seen: false };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mocks.auth = { user: null, loading: false };
  mocks.feed = [];
  mocks.loading = false;
  mocks.error = false;
  mocks.mark.mockResolvedValue({ status: "ok" });
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});

describe("AnnouncementInbox", () => {
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

  it("preserves legacy guest receipts and suppresses repeat popups across route remounts", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify([1301]));
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
    const view = render(<AnnouncementInbox />);
    fireEvent.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByRole("status")).toHaveTextContent("loading");
    mocks.loading = false;
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
    mocks.feed = [{ ...announcement(1701), content: "Edited body", updated_at: 2 }];
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
    mocks.feed = [{ ...announcement(1901), revision: 2, content: "Reannounced body" }];
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

  it("treats legacy guest IDs as revision one without suppressing a newer revision", async () => {
    localStorage.setItem("announcement-seen-ids", JSON.stringify([2001, "2002", "2003:2"]));
    mocks.feed = [announcement(2001), announcement(2002), { ...announcement(2003), revision: 2 }];
    const view = render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    mocks.feed = [{ ...announcement(2001), revision: 2 }, ...mocks.feed.slice(1)];
    view.rerender(<AnnouncementInbox guestsOnly />);
    await screen.findByRole("heading", { name: "Announcement 2001" });
    await waitFor(() => expect(JSON.parse(localStorage.getItem("announcement-seen-ids")!)).toEqual(expect.arrayContaining(["2001:1", "2001:2", "2002:1", "2003:2"])));
    expect(mocks.mark).not.toHaveBeenCalled();
    view.unmount();
    render(<AnnouncementInbox guestsOnly />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each([true, false])("isolates a stale receipt completion when the new receipt succeeds: %s", async (newSucceeded) => {
    const id = newSucceeded ? 2101 : 2201;
    mocks.auth.user = { user_id: id };
    const { promise: oldReceipt, resolve: resolveOld, reject: rejectOld } = Promise.withResolvers<{ status: string }>();
    mocks.mark.mockImplementation(({ revision }: { revision: number }) => revision === 1
      ? oldReceipt
      : newSucceeded ? Promise.resolve({ status: "ok" }) : Promise.reject(new Error("offline")));
    mocks.feed = [announcement(id)];
    const view = render(<AnnouncementInbox />);
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith({ id, revision: 1 }));
    // The same detail stays open while a newer revision arrives.
    mocks.feed = [{ ...announcement(id), revision: 2, content: "Updated while open" }];
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
});
