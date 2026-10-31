import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { queryClientWrapper } from "@/test/render";
import type { Announcement, AnnouncementPage, AnnouncementSummary } from "@/lib/api/announcements";
import { ApiError } from "@/lib/api/client";
import type * as ClientModule from "@/lib/api/client";
import AnnouncementsPage from "./page";

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn(), admin: true, userId: 1 }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ClientModule>();
  return { ...actual, api: { get: mocks.get, post: mocks.post, put: mocks.put, delete: mocks.remove } };
});
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: mocks.userId }, isAdmin: mocks.admin, loading: false }) }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string, values?: Record<string, unknown>) => values ? `${key}:${Object.values(values).join(":")}` : key }));
vi.mock("sonner", () => ({ toast: { success: vi.fn() } }));
vi.mock("@/components/layout/page-layout", () => ({ PageLayout: ({ children, actions }: { children: ReactNode; actions?: ReactNode }) => <main>{actions}{children}</main> }));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({ EntityMultiPicker: () => <div /> }));

const summary: AnnouncementSummary = { id: 11, title: "Summary title", group_ids: [], show_for_guests: true, pinned: true, revision: 3, created_at: 1, updated_at: 1 };

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

beforeEach(() => {
  mocks.admin = true;
  mocks.userId = 1;
  mocks.get.mockReset().mockResolvedValue({ items: [summary], total: 1, page: 1, page_size: 10 });
  mocks.post.mockReset();
  mocks.put.mockReset();
  mocks.remove.mockReset();
});

describe("announcement management", () => {
  it("loads summaries only, then fetches the full body before rendering an editable form", async () => {
    let resolveDetail!: (detail: Announcement) => void;
    const detail = new Promise<Announcement>((resolve) => { resolveDetail = resolve; });
    mocks.get.mockImplementation((path: string) => path === "/admin/announcements?page=1&page_size=10" ? Promise.resolve({ items: [summary], total: 1, page: 1, page_size: 10 }) : detail);
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    expect(mocks.get.mock.calls).toEqual([["/admin/announcements?page=1&page_size=10"]]);
    expect(screen.queryByRole("columnheader", { name: "content" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(screen.getByRole("status")).toHaveTextContent("loading");
    expect(screen.queryByLabelText("content")).not.toBeInTheDocument();
    await act(async () => { resolveDetail({ ...summary, title: "Fresh title", content: "Full body from detail endpoint" }); });
    expect(await screen.findByLabelText("content")).toHaveValue("Full body from detail endpoint");
    expect(screen.getByLabelText("announcementTitle")).toHaveValue("Fresh title");
    expect(mocks.get).toHaveBeenLastCalledWith("/admin/announcements/11");
  });

  it("shows detail access failures with retry, never a fake blank edit form", async () => {
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 1, page: 1, page_size: 10 }).mockRejectedValueOnce(new ApiError(404, "Not visible"));
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    fireEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Not visible");
    expect(screen.queryByLabelText("content")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
    mocks.get.mockResolvedValueOnce({ ...summary, content: "Recovered body" });
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(await screen.findByLabelText("content")).toHaveValue("Recovered body");
    expect(mocks.get).toHaveBeenCalledTimes(3);
  });

  it("uses the standard footer and retains the successful page while navigating, then reuses cached pages", async () => {
    const older = { ...summary, id: 12, title: "Older summary" };
    let resolvePage!: (page: AnnouncementPage) => void;
    const nextPage = new Promise<AnnouncementPage>((resolve) => { resolvePage = resolve; });
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 20, page: 1, page_size: 10 }).mockReturnValueOnce(nextPage);
    const wrapper = queryClientWrapper();
    const view = render(<AnnouncementsPage />, { wrapper });
    await screen.findByText("Summary title");
    expect(screen.getByText("total:20")).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveTextContent("perPage:10");
    expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    expect(screen.getByText("Summary title")).toBeInTheDocument();
    expect(screen.getByText("total:20")).toBeInTheDocument();
    expect(screen.getByText("2 / 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "previousPage" })).toBeDisabled();
    await act(async () => { resolvePage({ items: [older], total: 20, page: 2, page_size: 10 }); });
    await screen.findByText("Older summary");
    expect(screen.queryByText("Summary title")).not.toBeInTheDocument();
    expect(mocks.get).toHaveBeenLastCalledWith("/admin/announcements?page=2&page_size=10");
    expect(screen.getByRole("button", { name: "nextPage" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "previousPage" }));
    expect(screen.getByText("Summary title")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    expect(screen.getByText("Older summary")).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledTimes(2);
    view.unmount();
    render(<AnnouncementsPage />, { wrapper });
    expect(screen.getByText("Summary title")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    expect(screen.getByText("Older summary")).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledTimes(2);
  });

  it("retains the last successful rows and total when navigation fails, with retry available", async () => {
    const older = { ...summary, id: 12, title: "Older summary" };
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 20, page: 1, page_size: 10 })
      .mockRejectedValueOnce(new ApiError(503, "Page unavailable"));
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Page unavailable");
    expect(screen.getByText("Summary title")).toBeInTheDocument();
    expect(screen.getByText("total:20")).toBeInTheDocument();
    expect(screen.getByText("2 / 2")).toBeInTheDocument();
    mocks.get.mockResolvedValueOnce({ items: [older], total: 20, page: 2, page_size: 10 });
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await screen.findByText("Older summary");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(mocks.get).toHaveBeenLastCalledWith("/admin/announcements?page=2&page_size=10");
  });

  it.each([20, 50])("resets to the first page and requests the selected page size %i", async (pageSize) => {
    const user = userEvent.setup();
    const older = { ...summary, id: 12, title: "Older summary" };
    const resized = { ...summary, title: "Resized summary" };
    let resolvePage!: (page: AnnouncementPage) => void;
    const resizedPage = new Promise<AnnouncementPage>((resolve) => { resolvePage = resolve; });
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 51, page: 1, page_size: 10 })
      .mockResolvedValueOnce({ items: [older], total: 51, page: 2, page_size: 10 })
      .mockReturnValueOnce(resizedPage);
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    await screen.findByText("Older summary");
    await user.click(screen.getByRole("combobox"));
    for (const size of [10, 20, 50]) {
      expect(screen.getByRole("option", { name: `perPage:${size}` })).toBeInTheDocument();
    }
    await user.click(screen.getByRole("option", { name: `perPage:${pageSize}` }));
    await waitFor(() => expect(mocks.get).toHaveBeenLastCalledWith(`/admin/announcements?page=1&page_size=${pageSize}`));
    expect(screen.getByRole("combobox")).toHaveTextContent(`perPage:${pageSize}`);
    expect(screen.getByRole("button", { name: "previousPage" })).toBeDisabled();
    await act(async () => { resolvePage({ items: [resized], total: 51, page: 1, page_size: pageSize }); });
    await screen.findByText("Resized summary");
    expect(screen.queryByText("Older summary")).not.toBeInTheDocument();
    expect(screen.getByText(`1 / ${Math.ceil(51 / pageSize)}`)).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledTimes(3);
  });

  it("clamps to the last remaining page after deleting the only row on the final page", async () => {
    const older = { ...summary, id: 12, title: "Older summary" };
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 11, page: 1, page_size: 10 })
      .mockResolvedValueOnce({ items: [older], total: 11, page: 2, page_size: 10 })
      .mockResolvedValueOnce({ items: [], total: 10, page: 2, page_size: 10 })
      .mockResolvedValueOnce({ items: [summary], total: 10, page: 1, page_size: 10 });
    mocks.remove.mockResolvedValueOnce({ status: "ok" });
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    fireEvent.click(screen.getByRole("button", { name: "nextPage" }));
    await screen.findByText("Older summary");
    fireEvent.click(screen.getByRole("button", { name: "delete" }));
    fireEvent.click(screen.getByRole("button", { name: "confirm" }));
    await screen.findByText("Summary title");
    expect(mocks.remove).toHaveBeenCalledExactlyOnceWith("/admin/announcements/12");
    expect(mocks.get).toHaveBeenLastCalledWith("/admin/announcements?page=1&page_size=10");
    expect(screen.getByText("total:10")).toBeInTheDocument();
    expect(screen.getByText("1 / 1")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "nextPage" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "previousPage" })).toBeDisabled();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("clears open forms and isolates cached feed and detail data when the administrator changes", async () => {
    mocks.get.mockResolvedValueOnce({ items: [summary], total: 1, page: 1, page_size: 10 })
      .mockResolvedValueOnce({ ...summary, content: "First administrator's body" });
    const view = render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await screen.findByText("Summary title");
    fireEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(await screen.findByLabelText("content")).toHaveValue("First administrator's body");
    const nextSummary = { ...summary, title: "Next administrator's summary" };
    mocks.get.mockResolvedValueOnce({ items: [nextSummary], total: 1, page: 1, page_size: 10 })
      .mockResolvedValueOnce({ ...nextSummary, content: "Next administrator's body" });
    mocks.userId = 2;
    view.rerender(<AnnouncementsPage />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByText("Summary title")).not.toBeInTheDocument();
    await screen.findByText("Next administrator's summary");
    fireEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(screen.queryByDisplayValue("First administrator's body")).not.toBeInTheDocument();
    expect(await screen.findByLabelText("content")).toHaveValue("Next administrator's body");
    expect(mocks.get).toHaveBeenCalledTimes(4);
  });

  it("does not request admin data when the current user is not an administrator", async () => {
    mocks.admin = false;
    render(<AnnouncementsPage />, { wrapper: queryClientWrapper() });
    await waitFor(() => expect(screen.getByText("permissionDenied")).toBeInTheDocument());
    expect(mocks.get).not.toHaveBeenCalled();
  });
});
