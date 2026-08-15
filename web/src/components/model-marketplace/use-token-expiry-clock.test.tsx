import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ServerTimeAxis } from "@/lib/api/server-time";
import type { Token } from "@/lib/types";
import { useTokenExpiryClock } from "./use-token-expiry-clock";

function token(expiredAt: number): Token {
  return {
    id: 17,
    user_id: 7,
    key: "key-17",
    name: "Token 17",
    status: 1,
    expired_at: expiredAt,
    models: "",
    trace_enabled: false,
    trace_mode: "full",
    created_at: 1,
    updated_at: 1,
  };
}

describe("useTokenExpiryClock server-time axis", () => {
  afterEach(() => vi.useRealTimers());

  it("does not turn the unsynchronized client clock into an eligibility verdict", () => {
    vi.useFakeTimers();
    vi.setSystemTime(2_000_000);
    const axis = new ServerTimeAxis();

    const { result } = renderHook(() => useTokenExpiryClock([token(1_000)], axis));

    expect(result.current).toBeUndefined();
  });

  it.each([
    ["fast", 2_000_000],
    ["slow", 500_000],
  ] as const)("keeps expired_at equal to server_now valid with a %s client clock", (
    _name,
    clientNowMs,
  ) => {
    vi.useFakeTimers();
    vi.setSystemTime(clientNowMs);
    const axis = new ServerTimeAxis();
    axis.observe(1_000_000, clientNowMs);

    const { result } = renderHook(() => useTokenExpiryClock([token(1_000)], axis));

    expect(result.current).toBe(1_000);
  });

  it("moves past the inclusive expiry boundary on the next server second", () => {
    vi.useFakeTimers();
    vi.setSystemTime(2_000_000);
    const axis = new ServerTimeAxis();
    axis.observe(1_000_000, 2_000_000);
    const { result } = renderHook(() => useTokenExpiryClock([token(1_000)], axis));
    expect(result.current).toBe(1_000);

    act(() => vi.advanceTimersByTime(1_000));

    expect(result.current).toBe(1_001);
  });
});
