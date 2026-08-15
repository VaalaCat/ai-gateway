import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "./client";
import {
  SERVER_TIME_HEADER,
  ServerTimeAxis,
  tokenServerTimeAxis,
} from "./server-time";

describe("ServerTimeAxis", () => {
  it.each([
    ["fast client clock", 2_000_000, -1_000_000],
    ["slow client clock", 500_000, 500_000],
  ] as const)("maps the %s onto the received server second", (
    _name,
    receivedClientNowMs,
    expectedOffsetMs,
  ) => {
    const axis = new ServerTimeAxis();

    axis.observe(1_000_000, receivedClientNowMs);

    expect(axis.offsetMs()).toBe(expectedOffsetMs);
    expect(axis.nowSeconds(receivedClientNowMs)).toBe(1_000);
  });

  it("returns to unknown when a newer Token response omits valid metadata", () => {
    const axis = new ServerTimeAxis();
    axis.observe(1_000_000, 2_000_000, 10);

    axis.observeHeader("not-a-time", 3_000_000, 20);

    expect(axis.offsetMs()).toBeUndefined();
    expect(axis.nowSeconds(3_000_000)).toBeUndefined();
  });

  it("does not let an older concurrent response replace the newest sample", () => {
    const axis = new ServerTimeAxis();
    axis.observe(1_000_000, 2_000_000, 20, 2);

    axis.observe(2_000_000, 2_500_000, 30, 1);

    expect(axis.offsetMs()).toBe(-1_000_000);
    expect(axis.nowSeconds(2_000_000)).toBe(1_000);
  });
});

describe("API server-time metadata", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("records the response header at client receive time without changing JSON data", async () => {
    vi.spyOn(Date, "now").mockReturnValue(2_000_000);
    vi.spyOn(performance, "now").mockReturnValue(123);
    const observe = vi.spyOn(tokenServerTimeAxis, "observeHeader");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ id: 17, name: "Token 17" }),
      {
        status: 200,
        headers: {
          "content-type": "application/json",
          [SERVER_TIME_HEADER]: "1000000",
        },
      },
    )));

    const data = await api.get<{ id: number; name: string }>("/tokens/17");

    expect(data).toEqual({ id: 17, name: "Token 17" });
    expect(observe).toHaveBeenCalledWith("1000000", 2_000_000, 123, expect.any(Number));
  });

  it("does not update the Token time axis from failed or non-Token responses", async () => {
    const observe = vi.spyOn(tokenServerTimeAxis, "observeHeader");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(
        JSON.stringify({ error: "failed" }),
        { status: 500, headers: { [SERVER_TIME_HEADER]: "1000000" } },
      ))
      .mockResolvedValueOnce(new Response(
        JSON.stringify({ ok: true }),
        { status: 200, headers: { [SERVER_TIME_HEADER]: "1000000" } },
      ));
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.get("/tokens/17")).rejects.toThrow();
    await expect(api.get("/health")).resolves.toEqual({ ok: true });

    expect(observe).not.toHaveBeenCalled();
  });

  it("clears the axis when a successful Token read has no server-time header", async () => {
    vi.spyOn(Date, "now").mockReturnValue(2_000_000);
    vi.spyOn(performance, "now").mockReturnValue(123);
    const observe = vi.spyOn(tokenServerTimeAxis, "observeHeader");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ data: [], total: 0, page: 1, page_size: 2 }),
      { status: 200, headers: { "content-type": "application/json" } },
    )));

    await api.get("/tokens?page=1&page_size=2");

    expect(observe).toHaveBeenCalledWith(null, 2_000_000, 123, expect.any(Number));
  });
});
