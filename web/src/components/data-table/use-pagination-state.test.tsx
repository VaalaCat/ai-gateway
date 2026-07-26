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

  it("falls back for zero, negative, fractional, and invalid values", () => {
    for (const value of ["0", "-1", "1.5", "invalid"]) {
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

  it("normalizes the first page and default size out of the URL", () => {
    query = "page=3&page_size=50&search=term";
    const { result } = renderHook(() => usePaginationState(20));

    act(() => result.current[2](1, 20));

    expect(replace).toHaveBeenCalledWith("/items?search=term");
  });
});
