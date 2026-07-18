import { beforeEach, describe, expect, it, vi } from "vitest";

const { download, postRawJSON } = vi.hoisted(() => ({
  download: vi.fn(),
  postRawJSON: vi.fn(),
}));

vi.mock("./client", () => ({ api: { download, postRawJSON } }));

import {
  commitChannelImport,
  downloadChannelExport,
  previewChannelImport,
} from "./channel-transfer";

describe("channel transfer API", () => {
  beforeEach(() => {
    download.mockReset();
    postRawJSON.mockReset();
  });

  it("uses the same raw document for preview and commit", async () => {
    postRawJSON
      .mockResolvedValueOnce({ total: 1, ready: 1, failed: 0, items: [] })
      .mockResolvedValueOnce({ created: 1, items: [] });

    await previewChannelImport("/channels/import", "{\"version\":1}");
    await commitChannelImport("/channels/import", "{\"version\":1}");

    expect(postRawJSON).toHaveBeenNthCalledWith(1, "/channels/import?dry_run=true", "{\"version\":1}");
    expect(postRawJSON).toHaveBeenNthCalledWith(2, "/channels/import?dry_run=false", "{\"version\":1}");
  });

  it("propagates preview failures unchanged", async () => {
    const failure = new Error("invalid channel file");
    postRawJSON.mockRejectedValueOnce(failure);

    await expect(previewChannelImport("/channels/import", "bad")).rejects.toBe(failure);
  });

  it("downloads with the server filename and always releases the object URL", async () => {
    const click = vi.fn();
    const anchor = { href: "", download: "", click };
    const createElement = vi.spyOn(document, "createElement").mockReturnValue(anchor as unknown as HTMLAnchorElement);
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:channels");
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    download.mockResolvedValueOnce({ blob: new Blob(["{}"]), filename: "channels-20260716.json" });

    await downloadChannelExport("/channels/export", { mode: "ids", ids: [3, 5] });

    expect(download).toHaveBeenCalledWith("/channels/export", { selection: { mode: "ids", ids: [3, 5] } });
    expect(anchor).toMatchObject({ href: "blob:channels", download: "channels-20260716.json" });
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:channels");

    createElement.mockRestore();
    createObjectURL.mockRestore();
    revokeObjectURL.mockRestore();
  });

  it("falls back to a stable filename when the response omits one", async () => {
    const anchor = { href: "", download: "", click: vi.fn() };
    vi.spyOn(document, "createElement").mockReturnValueOnce(anchor as unknown as HTMLAnchorElement);
    vi.spyOn(URL, "createObjectURL").mockReturnValueOnce("blob:fallback");
    vi.spyOn(URL, "revokeObjectURL").mockImplementationOnce(() => undefined);
    download.mockResolvedValueOnce({ blob: new Blob(["{}"]), filename: null });

    await downloadChannelExport("/channels/export", { mode: "filter", filter: {} });

    expect(anchor.download).toBe("ai-gateway-channels.json");
  });
});
