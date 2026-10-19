import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Announcement } from "@/lib/api/announcements";
import { AnnouncementFormDialog } from "./_form-dialog";

const { create, update } = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { success: vi.fn() } }));
vi.mock("@/lib/api/announcements", () => ({
  useCreateAnnouncement: () => ({ mutateAsync: create, isPending: false }),
  useUpdateAnnouncement: () => ({ mutateAsync: update, isPending: false }),
}));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: ({ entity, value, onChange, disabled }: { entity: string; value: string[]; onChange: (ids: string[]) => void; disabled?: boolean }) => (
    <select multiple aria-label={entity} value={value} disabled={disabled} onChange={(event) => onChange(Array.from(event.currentTarget.selectedOptions, (option) => option.value))}>
      <option value="7">Group seven</option><option value="9">Group nine</option>
    </select>
  ),
}));

const initial: Announcement = { id: 12, title: "Existing", content: "Old content", group_ids: [7], show_for_guests: false, pinned: true, revision: 3, created_at: 1, updated_at: 1 };

describe("announcement form", () => {
  beforeEach(() => {
    create.mockReset().mockResolvedValue(undefined);
    update.mockReset().mockResolvedValue(undefined);
  });

  it("creates for all logged-in users with guest visibility enabled by default", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    render(<AnnouncementFormDialog open onOpenChange={close} />);
    expect(screen.getByRole("switch", { name: "showForGuests" })).toBeChecked();
    expect(screen.queryByRole("switch", { name: "reannounce" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("announcementTitle"), "  Hello  ");
    await user.type(screen.getByLabelText("content"), "  Plain text\nSecond line  ");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(create).toHaveBeenCalledWith({ title: "Hello", content: "Plain text\nSecond line", group_ids: [], show_for_guests: true, pinned: false });
    expect(close).toHaveBeenCalledWith(false);
  });

  it("supports multiple groups and an independent guest switch", async () => {
    const user = userEvent.setup();
    render(<AnnouncementFormDialog open onOpenChange={() => {}} />);
    await user.type(screen.getByLabelText("announcementTitle"), "Groups");
    await user.type(screen.getByLabelText("content"), "Content");
    await user.selectOptions(screen.getByLabelText("user-group"), ["7", "9"]);
    await user.click(screen.getByRole("switch", { name: "showForGuests" }));
    await user.click(screen.getByRole("switch", { name: "pinned" }));
    expect(screen.getByRole("switch", { name: "pinned" })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(create).toHaveBeenCalledWith({ title: "Groups", content: "Content", group_ids: [7, 9], show_for_guests: false, pinned: true });
  });

  it("edits the same id and submits all visibility fields", async () => {
    const user = userEvent.setup();
    render(<AnnouncementFormDialog open initial={initial} onOpenChange={() => {}} />);
    expect(screen.getByRole("switch", { name: "pinned" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "reannounce" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "reannounce" })).toHaveAccessibleDescription("reannounceHint");
    await user.clear(screen.getByLabelText("content"));
    await user.type(screen.getByLabelText("content"), "Updated");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(update).toHaveBeenCalledWith({ id: 12, title: "Existing", content: "Updated", group_ids: [7], show_for_guests: false, pinned: true, reannounce: false });
    expect(create).not.toHaveBeenCalled();
  });

  it("rejects whitespace-only text before issuing a mutation", async () => {
    const user = userEvent.setup();
    render(<AnnouncementFormDialog open onOpenChange={() => {}} />);
    await user.type(screen.getByLabelText("announcementTitle"), "   ");
    await user.type(screen.getByLabelText("content"), "Content");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("required");
    expect(create).not.toHaveBeenCalled();
  });

  it("counts Unicode characters consistently with the backend", async () => {
    const user = userEvent.setup();
    render(<AnnouncementFormDialog open initial={{ ...initial, title: "🎉".repeat(200) }} onOpenChange={() => {}} />);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(update).toHaveBeenCalledWith(expect.objectContaining({ title: "🎉".repeat(200) }));
  });

  it("rejects content exceeding the backend character limits", async () => {
    const user = userEvent.setup();
    render(<AnnouncementFormDialog open initial={{ ...initial, title: "a".repeat(201) }} onOpenChange={() => {}} />);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("tooLong");
    expect(update).not.toHaveBeenCalled();
  });

  it("retains text, pinning, and the chosen reannouncement option when saving fails", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    update.mockRejectedValue(new Error("Invalid group"));
    render(<AnnouncementFormDialog open initial={initial} onOpenChange={close} />);
    await user.click(screen.getByRole("switch", { name: "reannounce" }));
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid group");
    expect(screen.getByLabelText("content")).toHaveValue(initial.content);
    expect(screen.getByRole("switch", { name: "pinned" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "reannounce" })).toBeChecked();
    expect(close).not.toHaveBeenCalled();
  });
});
