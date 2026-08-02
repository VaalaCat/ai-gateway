import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useObsRange } from "./use-obs-range";

const mocks = vi.hoisted(() => ({
  pathname: "/dashboard",
  replace: vi.fn(),
  searchParams: new URLSearchParams(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => mocks.pathname,
  useRouter: () => ({ replace: mocks.replace }),
  useSearchParams: () => mocks.searchParams,
}));

beforeEach(() => {
  mocks.pathname = "/dashboard";
  mocks.replace.mockReset();
  mocks.searchParams = new URLSearchParams();
});

describe("useObsRange", () => {
  it("returns day immediately for a long hour URL and repairs gran without losing query", async () => {
    mocks.searchParams = new URLSearchParams(
      "start=100&end=700000&gran=hour&model=gpt-5&top_n=10",
    );

    const { result } = renderHook(() => useObsRange());

    expect(result.current.range).toEqual({ start: 100, end: 700000, gran: "day" });
    await waitFor(() => {
      expect(mocks.replace).toHaveBeenCalledWith(
        "/dashboard?start=100&end=700000&gran=day&model=gpt-5&top_n=10",
        { scroll: false },
      );
    });
  });

  it("keeps an hour URL at the seven-day boundary without replacing it", () => {
    mocks.searchParams = new URLSearchParams(
      `start=100&end=${100 + 7 * 86_400}&gran=hour&model=gpt-5`,
    );

    const { result } = renderHook(() => useObsRange());

    expect(result.current.range.gran).toBe("hour");
    expect(mocks.replace).not.toHaveBeenCalled();
  });
});
