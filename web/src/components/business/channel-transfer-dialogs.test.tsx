import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { commitImport, downloadExport, previewImport, toastError, toastSuccess } = vi.hoisted(() => ({
  commitImport: vi.fn(),
  downloadExport: vi.fn(),
  previewImport: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/api/channel-transfer", () => ({
  commitChannelImport: commitImport,
  downloadChannelExport: downloadExport,
  previewChannelImport: previewImport,
}));

vi.mock("sonner", () => ({ toast: { error: toastError, success: toastSuccess } }));
vi.mock("@/lib/api/error-toast", () => ({
  formatErrorToast: (_error: unknown, fallback: string) => fallback,
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: { count?: number }) => {
    const labels: Record<string, string> = {
      cancel: "Cancel",
      confirmExport: "Export",
      confirmImport: "Import",
      exportDescription: "Choose channels to export",
      exportFailed: "Export failed",
      exportScope: "Scope",
      exportTitle: "Export channels",
      exported: "Exported",
      exporting: "Exporting",
      failed: `${values?.count ?? 0} failed`,
      file: "File",
      fileLimit: "Up to 16 MiB",
      fileTooLarge: "File too large",
      filtered: "Current filter",
      filteredDescription: "All matching channels",
      importDescription: "Preview before importing",
      importFailed: "Import failed",
      importTitle: "Import channels",
      imported: `Imported ${values?.count ?? 0}`,
      importing: "Importing",
      itemFailed: "Failed",
      itemReady: "Ready",
      plaintextExportDescription: "Contains plaintext credentials",
      plaintextImportDescription: "Reads plaintext credentials",
      plaintextTitle: "Sensitive file",
      previewFailed: "Preview failed",
      previewing: "Previewing",
      ready: `${values?.count ?? 0} ready`,
      renamed: "Renamed",
      selected: `${values?.count ?? 0} selected`,
      selectedDescription: "Only selected channels",
      total: `${values?.count ?? 0} total`,
    };
    return labels[key] ?? key;
  },
}));

import { ChannelExportDialog, ChannelImportDialog } from "./channel-transfer-dialogs";

function channelFile(contents = "{\"version\":1}", size?: number) {
  const file = new File([contents], "channels.json", { type: "application/json" });
  Object.defineProperty(file, "text", { value: vi.fn().mockResolvedValue(contents) });
  if (size !== undefined) Object.defineProperty(file, "size", { value: size });
  return file;
}

describe("ChannelImportDialog", () => {
  beforeEach(() => {
    commitImport.mockReset();
    previewImport.mockReset();
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("previews and commits a valid file before refreshing the list", async () => {
    const user = userEvent.setup();
    const onImported = vi.fn().mockResolvedValue(undefined);
    const onOpenChange = vi.fn();
    previewImport.mockResolvedValueOnce({
      kind: "admin_channels", total: 1, ready: 1, failed: 0,
      items: [{ index: 0, source_name: "Primary", final_name: "Primary-2", warnings: [{ code: "renamed", message: "renamed" }] }],
    });
    commitImport.mockResolvedValueOnce({ kind: "admin_channels", created: 1, items: [] });

    render(<ChannelImportDialog open path="/channels/import" onOpenChange={onOpenChange} onImported={onImported} />);
    await user.upload(screen.getByLabelText("File"), channelFile());

    expect(await screen.findByText("Primary-2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() => expect(commitImport).toHaveBeenCalledWith("/channels/import", "{\"version\":1}"));
    expect(onImported).toHaveBeenCalledOnce();
    expect(toastSuccess).toHaveBeenCalledWith("Imported 1");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("keeps import disabled when preview reports an item error", async () => {
    const user = userEvent.setup();
    previewImport.mockResolvedValueOnce({
      kind: "admin_channels", total: 1, ready: 0, failed: 1,
      items: [{ index: 0, source_name: "Broken", final_name: "", warnings: [], error: { code: "invalid", message: "Missing key" } }],
    });

    render(<ChannelImportDialog open path="/channels/import" onOpenChange={vi.fn()} onImported={vi.fn()} />);
    await user.upload(screen.getByLabelText("File"), channelFile());

    expect(await screen.findByText("Missing key")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
    expect(commitImport).not.toHaveBeenCalled();
  });

  it("rejects an oversized file locally and allows selecting the same valid file twice", async () => {
    const input = () => screen.getByLabelText("File");
    previewImport.mockResolvedValue({ kind: "admin_channels", total: 0, ready: 0, failed: 0, items: [] });

    render(<ChannelImportDialog open path="/channels/import" onOpenChange={vi.fn()} onImported={vi.fn()} />);
    fireEvent.change(input(), { target: { files: [channelFile("{}", 16 * 1024 * 1024 + 1)] } });
    expect(toastError).toHaveBeenCalledWith("File too large");
    expect(previewImport).not.toHaveBeenCalled();

    const file = channelFile();
    fireEvent.change(input(), { target: { files: [file] } });
    await waitFor(() => expect(previewImport).toHaveBeenCalledTimes(1));
    fireEvent.change(input(), { target: { files: [file] } });
    await waitFor(() => expect(previewImport).toHaveBeenCalledTimes(2));
  });
});

describe("ChannelExportDialog", () => {
  beforeEach(() => {
    downloadExport.mockReset();
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("exports selected IDs when the dialog opens with a selection", async () => {
    const user = userEvent.setup();
    downloadExport.mockResolvedValueOnce(undefined);

    render(<ChannelExportDialog open path="/channels/export" selectedIds={[2, 7]} filter={{ search: "openai" }} onOpenChange={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Export" }));

    expect(downloadExport).toHaveBeenCalledWith("/channels/export", { mode: "ids", ids: [2, 7] });
  });

  it("uses the current filter when there is no selection", async () => {
    const user = userEvent.setup();
    downloadExport.mockResolvedValueOnce(undefined);

    render(<ChannelExportDialog open path="/channels/export" selectedIds={[]} filter={{ status: "enabled" }} onOpenChange={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Export" }));

    expect(downloadExport).toHaveBeenCalledWith("/channels/export", { mode: "filter", filter: { status: "enabled" } });
  });

  it("keeps the dialog open and reports export failures", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    downloadExport.mockRejectedValueOnce(new Error("network"));

    render(<ChannelExportDialog open path="/channels/export" selectedIds={[1]} filter={{}} onOpenChange={onOpenChange} />);
    await user.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Export failed"));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});
