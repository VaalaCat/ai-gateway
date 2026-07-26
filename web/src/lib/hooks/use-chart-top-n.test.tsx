import { act, renderHook } from "@testing-library/react";
import { renderToString } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChartTopN } from "./use-chart-top-n";

const navigation = vi.hoisted(() => ({
  pathname: "/dashboard",
  query: "",
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => navigation.pathname,
  useRouter: () => ({ replace: navigation.replace }),
  useSearchParams: () => new URLSearchParams(navigation.query),
}));

beforeEach(() => {
  navigation.pathname = "/dashboard";
  navigation.query = "";
  navigation.replace.mockReset();
  window.localStorage.clear();
});

describe("useChartTopN", () => {
  it("uses a legal URL value before the remembered page preference", () => {
    navigation.query = "top_n=10";
    window.localStorage.setItem("chartTopN:7:/dashboard", "20");

    const { result } = renderHook(() => useChartTopN(7, "/dashboard"));

    expect(result.current[0]).toBe(10);
  });

  it("isolates remembered values by user and pathname", () => {
    window.localStorage.setItem("chartTopN:7:/dashboard", "10");
    window.localStorage.setItem("chartTopN:8:/dashboard", "20");
    window.localStorage.setItem("chartTopN:7:/billing", "5");

    const { result, rerender } = renderHook(
      ({ userId, pathname }) => useChartTopN(userId, pathname),
      { initialProps: { userId: 7, pathname: "/dashboard" } },
    );
    expect(result.current[0]).toBe(10);

    rerender({ userId: 8, pathname: "/dashboard" });
    expect(result.current[0]).toBe(20);

    rerender({ userId: 7, pathname: "/billing" });
    expect(result.current[0]).toBe(5);
  });

  it("uses a stable default during server rendering", () => {
    navigation.query = "top_n=20";
    window.localStorage.setItem("chartTopN:7:/dashboard", "10");

    function Probe() {
      const [topN] = useChartTopN(7, "/dashboard");
      return <span>{topN}</span>;
    }

    expect(renderToString(<Probe />)).toContain(">5<");
  });

  it("falls back to 5 for an illegal URL value instead of stored state", () => {
    navigation.query = "top_n=7";
    window.localStorage.setItem("chartTopN:7:/dashboard", "20");

    const { result } = renderHook(() => useChartTopN(7, "/dashboard"));

    expect(result.current[0]).toBe(5);
  });

  it("falls back to 5 when browser storage is unavailable", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage disabled");
    });
    try {
      const { result } = renderHook(() => useChartTopN(7, "/dashboard"));

      expect(result.current[0]).toBe(5);
    } finally {
      getItem.mockRestore();
    }
  });

  it("updates the URL without dropping other params and writes the exact storage key", () => {
    navigation.query = "model=gpt-5&user_id=9";
    const { result } = renderHook(() => useChartTopN(7, "/dashboard"));

    act(() => result.current[1](20));

    expect(navigation.replace).toHaveBeenCalledWith(
      "/dashboard?model=gpt-5&user_id=9&top_n=20",
      { scroll: false },
    );
    expect(window.localStorage.getItem("chartTopN:7:/dashboard")).toBe("20");
  });

  it("re-resolves browser navigation when an existing top_n changes", () => {
    navigation.query = "model=gpt-5&top_n=10";
    const { result, rerender } = renderHook(() => useChartTopN(7, "/dashboard"));
    expect(result.current[0]).toBe(10);

    navigation.query = "model=gpt-5&top_n=20";
    rerender();
    expect(result.current[0]).toBe(20);
  });

  it("replaces an existing top_n without duplicating it", () => {
    navigation.query = "top_n=10&model=gpt-5";
    const { result } = renderHook(() => useChartTopN(7, "/dashboard"));

    act(() => result.current[1](5));

    expect(navigation.replace).toHaveBeenCalledWith(
      "/dashboard?top_n=5&model=gpt-5",
      { scroll: false },
    );
  });
});
