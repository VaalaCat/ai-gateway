import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRequestLog } from "@/lib/api/api-logs";
import { APILogDebugFileButton } from "./api-log-debug-file-button";

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
vi.mock("@/lib/api/api-log-debug-file", () => ({
  downloadAPIRequestLogDebugFile: download,
}));

const log = {
  id: 7,
  request_id: "req-api-debug",
  status_code: 502,
  has_trace: true,
} as APIRequestLog;

describe("APILogDebugFileButton", () => {
  beforeEach(() => {
    download.mockReset();
    toastError.mockReset();
  });

  it.each(["admin", "portal"] as const)("downloads the selected log from the %s Trace scope", async (scope) => {
    download.mockResolvedValueOnce(undefined);
    render(<APILogDebugFileButton log={log} scope={scope} />);

    expect(download).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));

    expect(download).toHaveBeenCalledOnce();
    expect(download).toHaveBeenCalledWith(log, scope);
  });

  it("disables repeated downloads while the Trace file is being assembled", async () => {
    let resolve!: () => void;
    download.mockReturnValueOnce(new Promise<void>((done) => { resolve = done; }));
    render(<APILogDebugFileButton log={log} scope="admin" />);

    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));
    expect(screen.getByRole("button", { name: "Download debug file" })).toBeDisabled();

    resolve();
    await waitFor(() => expect(screen.getByRole("button", { name: "Download debug file" })).toBeEnabled());
  });

  it("reports a Trace download failure and restores the action", async () => {
    download.mockRejectedValueOnce(new Error("trace unavailable"));
    render(<APILogDebugFileButton log={log} scope="admin" />);

    await userEvent.click(screen.getByRole("button", { name: "Download debug file" }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Could not download debug file"));
    expect(screen.getByRole("button", { name: "Download debug file" })).toBeEnabled();
  });
});
