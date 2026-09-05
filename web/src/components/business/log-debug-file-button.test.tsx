import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { UsageLog } from "@/lib/types";
import { LogDebugFileButton } from "./log-debug-file-button";

const { download, toastError } = vi.hoisted(() => ({
  download: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    downloadDebugFile: "Download debug file",
    debugFileDownloadFailed: "Could not download debug file",
  } as Record<string, string>)[key] ?? key,
}));
vi.mock("sonner", () => ({ toast: { error: toastError } }));
vi.mock("@/lib/api/log-debug-file", () => ({ downloadLogDebugFile: download }));

const log = {
  id: 7,
  request_id: "req-debug",
  status: 0,
  has_trace: true,
} as UsageLog;

describe("LogDebugFileButton", () => {
  beforeEach(() => {
    download.mockReset();
    toastError.mockReset();
  });

  it("downloads only after the operator clicks", async () => {
    download.mockResolvedValueOnce(undefined);
    render(<LogDebugFileButton log={log} />);

    expect(download).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));

    expect(download).toHaveBeenCalledOnce();
    expect(download).toHaveBeenCalledWith(log);
  });

  it("disables the action while the debug file is being assembled", async () => {
    let resolve!: () => void;
    download.mockReturnValueOnce(new Promise<void>((done) => { resolve = done; }));
    render(<LogDebugFileButton log={log} />);

    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));
    expect(screen.getByRole("button", { name: "Download debug file" })).toBeDisabled();

    resolve();
    await waitFor(() => expect(screen.getByRole("button", { name: "Download debug file" })).toBeEnabled());
  });

  it("reports failure without hiding the action", async () => {
    download.mockRejectedValueOnce(new Error("trace unavailable"));
    render(<LogDebugFileButton log={log} />);

    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Could not download debug file"));
    expect(screen.getByRole("button", { name: "Download debug file" })).toBeEnabled();
  });
});
