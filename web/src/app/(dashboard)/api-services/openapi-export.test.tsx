import { beforeEach, describe, expect, it, vi } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("@/lib/api/client", () => ({ api: { get } }));

import { downloadServiceOpenAPI } from "@/lib/api/api-services";

describe("OpenAPI service export", () => {
  beforeEach(() => {
    get.mockReset();
    vi.restoreAllMocks();
  });

  it("downloads the standard OpenAPI JSON with a service-derived filename and cleans up DOM state", async () => {
    const exported = { openapi: "3.1.0", info: { title: "Weather", version: "1" }, servers: [{ url: "https://gateway.example.test/v1/api/weather" }] };
    let blob: Blob | undefined;
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    vi.spyOn(URL, "createObjectURL").mockImplementation((value) => { blob = value; return "blob:openapi"; });
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    get.mockResolvedValueOnce({ export: exported, service: { id: 7 }, routes: [{ id: 9 }] });

    await downloadServiceOpenAPI(7, "weather");

    expect(get).toHaveBeenCalledWith("/admin/api-services/7/openapi");
    expect(blob?.type).toBe("application/json");
    expect(JSON.parse(await blob!.text())).toEqual(exported);
    expect(await blob!.text()).not.toContain('"service"');
    expect(document.querySelector('a[download="weather.openapi.json"]')).not.toBeInTheDocument();
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:openapi");
  });

  it("removes the anchor and revokes the object URL exactly once when click throws", async () => {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:click-error");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => { throw new Error("click failed"); });
    get.mockResolvedValueOnce({ export: { openapi: "3.1.0" } });

    await expect(downloadServiceOpenAPI(7, "weather")).rejects.toThrow("click failed");

    expect(document.querySelector('a[download="weather.openapi.json"]')).not.toBeInTheDocument();
    expect(revoke).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:click-error");
  });

  it("falls back to removeChild and revokes the object URL when anchor.remove throws", async () => {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:remove-error");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "remove").mockImplementation(() => { throw new Error("remove failed"); });
    get.mockResolvedValueOnce({ export: { openapi: "3.1.0" } });

    await expect(downloadServiceOpenAPI(7, "weather")).rejects.toThrow("remove failed");

    expect(document.querySelector('a[download="weather.openapi.json"]')).not.toBeInTheDocument();
    expect(revoke).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:remove-error");
  });

  it("preserves a createElement error and revokes the object URL exactly once", async () => {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:create-error");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(document, "createElement").mockImplementation(() => { throw new Error("create failed"); });
    get.mockResolvedValueOnce({ export: { openapi: "3.1.0" } });

    await expect(downloadServiceOpenAPI(7, "weather")).rejects.toThrow("create failed");

    expect(revoke).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:create-error");
  });

  it("preserves an appendChild error, removes the detached anchor, and revokes the object URL once", async () => {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:append-error");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(document.body, "appendChild").mockImplementation(() => { throw new Error("append failed"); });
    get.mockResolvedValueOnce({ export: { openapi: "3.1.0" } });

    await expect(downloadServiceOpenAPI(7, "weather")).rejects.toThrow("append failed");

    expect(document.querySelector('a[download="weather.openapi.json"]')).not.toBeInTheDocument();
    expect(revoke).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:append-error");
  });
});
