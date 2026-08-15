import { describe, expect, it, vi } from "vitest";

import { readRouteWorkspaceLoginToken } from "./route-workspace-login";

function response(options: { ok: boolean; status: number; statusText: string; json?: () => Promise<unknown>; text?: () => Promise<string> }) {
  return {
    ok: () => options.ok,
    status: () => options.status,
    statusText: () => options.statusText,
    json: options.json ?? vi.fn().mockResolvedValue({}),
    text: options.text ?? vi.fn().mockResolvedValue("unused"),
  };
}

describe("readRouteWorkspaceLoginToken", () => {
  it("returns a successful token without reading response text", async () => {
    const text = vi.fn().mockResolvedValue("sentinel-token-success");
    const token = await readRouteWorkspaceLoginToken(response({
      ok: true,
      status: 200,
      statusText: "OK",
      json: vi.fn().mockResolvedValue({ token: "sentinel-token-success" }),
      text,
    }));

    expect(token).toBe("sentinel-token-success");
    expect(text).not.toHaveBeenCalled();
  });

  it("reports only status metadata when a failed body contains credentials", async () => {
    const secretBody = "token=sentinel-token-failure password=sentinel-password";
    const text = vi.fn().mockResolvedValue(secretBody);
    const result = readRouteWorkspaceLoginToken(response({ ok: false, status: 401, statusText: "Unauthorized", text }));

    await expect(result).rejects.toThrow("Route workspace login failed (401 Unauthorized)");
    await expect(result).rejects.not.toThrow(/sentinel-token-failure|sentinel-password/);
    expect(text).not.toHaveBeenCalled();
  });

  it.each([
    ["malformed JSON", vi.fn().mockRejectedValue(new Error("sentinel-token-malformed"))],
    ["missing token", vi.fn().mockResolvedValue({ message: "sentinel-token-missing" })],
  ])("uses a generic error for a 200 response with %s", async (_case, json) => {
    const result = readRouteWorkspaceLoginToken(response({ ok: true, status: 200, statusText: "OK", json }));
    await expect(result).rejects.toThrow("Route workspace login returned an invalid response");
    await expect(result).rejects.not.toThrow(/sentinel-token/);
  });
});
