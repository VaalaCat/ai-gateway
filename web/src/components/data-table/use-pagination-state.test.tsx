import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useFilterState } from "./use-filter-state";
import { usePaginationState } from "./use-pagination-state";

const replace = vi.fn();
let query = "";

vi.mock("next/navigation", () => ({
  usePathname: () => "/items",
  useRouter: () => ({ replace }),
  useSearchParams: () => new URLSearchParams(query),
}));

describe("URL-backed table state", () => {
  beforeEach(() => {
    query = "";
    replace.mockReset();
  });

  it("reads valid pagination values from the URL", () => {
    query = "page=3&page_size=50";
    const { result } = renderHook(() => usePaginationState(20));

    expect(result.current.slice(0, 2)).toEqual([3, 50]);
  });

  it("reads prefixed pagination keys and preserves unrelated URL state when updating them", () => {
    query = "id=1&route_page=2&route_page_size=50&upstream_page=3";
    const { result } = renderHook(() =>
      usePaginationState(20, {
        pageKey: "route_page",
        pageSizeKey: "route_page_size",
      }),
    );

    expect(result.current.slice(0, 2)).toEqual([2, 50]);

    act(() => result.current[2](1, 20));

    expect(replace).toHaveBeenCalledWith("/items?id=1&upstream_page=3");
  });

  it.each(["", "0", "-1", "1.5", "1e3", "0x10", "+7", " 7", "01", "9007199254740992", "invalid"])(
    "falls back to defaults for missing or invalid prefixed pagination value %s",
    (value) => {
      query = value
        ? `route_page=${value}&route_page_size=${value}`
        : "unrelated_page=3";
      const { result, unmount } = renderHook(() =>
        usePaginationState(20, {
          pageKey: "route_page",
          pageSizeKey: "route_page_size",
        }),
      );

      expect(result.current.slice(0, 2)).toEqual([1, 20]);
      unmount();
    },
  );

  it("falls back for non-decimal, unsafe, zero, negative, fractional, and invalid values", () => {
    for (const value of ["0", "-1", "1.5", "1e3", "0x10", "+7", " 7", "01", "9007199254740992", "invalid"]) {
      query = `page=${value}&page_size=${value}`;
      const { result, unmount } = renderHook(() => usePaginationState(20));
      expect(result.current.slice(0, 2)).toEqual([1, 20]);
      unmount();
    }
  });

  it("removes page while preserving page size when a filter changes", () => {
    query = "page=4&page_size=50&search=old";
    const spec = { search: { kind: "text" as const } };
    const { result } = renderHook(() => useFilterState(spec));

    act(() => result.current[1]({ search: "new" }));

    expect(replace).toHaveBeenCalledWith("/items?page_size=50&search=new");
  });

  it("resets only the configured page key when a filter changes", () => {
    query = "id=1&route_search=old&route_page=3&upstream_page=3";
    const spec = { route_search: { kind: "text" as const } };
    const { result } = renderHook(() =>
      useFilterState(spec, { resetPageKey: "route_page" }),
    );

    act(() => result.current[1]({ route_search: "forecast" }));

    expect(replace).toHaveBeenCalledWith(
      "/items?id=1&route_search=forecast&upstream_page=3",
    );
  });

  it("reads both date bounds when the URL state spec includes a time field", () => {
    query = "start=1768867200&end=1768953599&model_name=gpt-5";
    const spec = {
      time: { kind: "time" as const },
      model_name: { kind: "text" as const },
    };

    const { result } = renderHook(() => useFilterState(spec));

    expect(result.current[0]).toEqual({
      start: 1_768_867_200,
      end: 1_768_953_599,
      model_name: "gpt-5",
    });
  });

  it.each(["1e3", "0x10", "+7", " 7", "01", "1.5", "-1", "9007199254740992"])(
    "does not coerce invalid time decimal %s",
    (value) => {
      query = new URLSearchParams({ start: value, end: value }).toString();
      const spec = { time: { kind: "time" as const } };

      const { result } = renderHook(() => useFilterState(spec));

      expect(result.current[0]).toEqual({});
    },
  );

  it("normalizes the first page and default size out of the URL", () => {
    query = "page=3&page_size=50&search=term";
    const { result } = renderHook(() => usePaginationState(20));

    act(() => result.current[2](1, 20));

    expect(replace).toHaveBeenCalledWith("/items?search=term");
  });
});
