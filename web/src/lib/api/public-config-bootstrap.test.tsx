import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";
import { restorePublicConfig, usePublicConfig, type PublicConfig } from "./system";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./client", () => ({ api: { get } }));
const storageKey = "ai-gateway:public-config:v1";
const cached: PublicConfig = { ...SITE_BRANDING_DEFAULTS, site_name: "Cached brand", site_title: "Cached title", site_icon_url: "/brand.svg", registration_enabled: false, invite_enabled: false, invite_user_max_codes: 5 };

beforeEach(() => { localStorage.clear(); get.mockReset(); });

describe("public branding bootstrap", () => {
  it("keeps the cached brand visible during slow revalidation and replaces it with the current server brand", async () => {
    localStorage.setItem(storageKey, JSON.stringify(cached));
    const client = createTestQueryClient();
    restorePublicConfig(client);
    let resolve!: (value: PublicConfig) => void;
    get.mockReturnValue(new Promise<PublicConfig>((done) => { resolve = done; }));
    const { result } = renderHook(() => usePublicConfig(), { wrapper: queryClientWrapper(client) });
    expect(result.current.data?.site_name).toBe("Cached brand");
    await waitFor(() => expect(result.current.isFetching).toBe(true));
    expect(result.current.data?.site_icon_url).toBe("/brand.svg");
    const current = { ...cached, site_name: "Current server brand" };
    await act(async () => { resolve(current); });
    await waitFor(() => expect(result.current.data?.site_name).toBe("Current server brand"));
    expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(current);
  });

  it.each(["not-json", JSON.stringify({ ...cached, site_icon_url: "javascript:alert(1)" }), JSON.stringify({ site_name: "incomplete" })])("does not seed untrusted or incomplete browser snapshots: %s", (value) => {
    localStorage.setItem(storageKey, value);
    const client = createTestQueryClient();
    restorePublicConfig(client);
    expect(client.getQueryData(["public-config"])).toBeUndefined();
  });

  it("does not replace a response already loaded from the server with an older browser snapshot", () => {
    localStorage.setItem(storageKey, JSON.stringify(cached));
    const client = createTestQueryClient();
    client.setQueryData(["public-config"], { ...cached, site_name: "Server brand" });
    restorePublicConfig(client);
    expect(client.getQueryData<PublicConfig>(["public-config"])?.site_name).toBe("Server brand");
  });
});
