import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSearchParamPatch } from "./use-search-param-patch";

const state = vi.hoisted(() => ({
  query: "",
  pathname: "/items",
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => state.pathname,
  useRouter: () => ({ replace: state.replace }),
  useSearchParams: () => new URLSearchParams(state.query),
}));

describe("useSearchParamPatch", () => {
  beforeEach(() => {
    state.query = "token_id=1&page=3";
    state.pathname = "/items";
    state.replace.mockReset();
  });

  it("merges consecutive patches against the latest pending target", () => {
    const { result } = renderHook(() => useSearchParamPatch());

    act(() => {
      result.current({ provider: "OpenAI", page: undefined });
      result.current({ token_id: 2, page: undefined });
    });

    expect(state.replace).toHaveBeenNthCalledWith(
      1,
      "/items?token_id=1&provider=OpenAI",
    );
    expect(state.replace).toHaveBeenNthCalledWith(
      2,
      "/items?token_id=2&provider=OpenAI",
    );
  });

  it("keeps the latest target across an intermediate commit and resets on external navigation", () => {
    const { result, rerender } = renderHook(() => useSearchParamPatch());

    act(() => {
      result.current({ provider: "OpenAI", page: undefined });
      result.current({ token_id: 2, page: undefined });
    });
    state.query = "token_id=1&provider=OpenAI";
    rerender();

    act(() => result.current({ page_size: 50 }));
    expect(state.replace).toHaveBeenLastCalledWith(
      "/items?token_id=2&provider=OpenAI&page_size=50",
    );

    state.query = "kind=routing";
    rerender();
    act(() => result.current({ search: "claude" }));
    expect(state.replace).toHaveBeenLastCalledWith(
      "/items?kind=routing&search=claude",
    );
  });

  it("clears a fully committed target so back navigation becomes authoritative", () => {
    const { result, rerender } = renderHook(() => useSearchParamPatch());

    act(() => result.current({ provider: "OpenAI", page: undefined }));
    state.query = "token_id=1&provider=OpenAI";
    rerender();
    state.query = "token_id=1&page=3";
    rerender();

    act(() => result.current({ kind: "real" }));
    expect(state.replace).toHaveBeenLastCalledWith(
      "/items?token_id=1&page=3&kind=real",
    );
  });

  it("does not leak pending targets across unmounts", () => {
    const first = renderHook(() => useSearchParamPatch());
    act(() => first.result.current({ provider: "OpenAI" }));
    first.unmount();

    const second = renderHook(() => useSearchParamPatch());
    act(() => second.result.current({ kind: "routing" }));

    expect(state.replace).toHaveBeenLastCalledWith(
      "/items?token_id=1&page=3&kind=routing",
    );
  });
});
