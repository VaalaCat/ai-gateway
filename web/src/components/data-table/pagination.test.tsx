import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DataTablePagination } from "./pagination";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

afterEach(() => vi.useRealTimers());

describe("DataTablePagination accessibility", () => {
  it("names both direction buttons and disables them on the only page", () => {
    render(
      <DataTablePagination
        page={1}
        pageSize={20}
        pageCount={1}
        onPaginationChange={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "previousPage" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "nextPage" })).toBeDisabled();
  });

  it("keeps both named buttons enabled on a middle page", () => {
    const onPaginationChange = vi.fn();
    render(
      <DataTablePagination
        page={2}
        pageSize={20}
        pageCount={3}
        onPaginationChange={onPaginationChange}
      />,
    );

    expect(screen.getByRole("button", { name: "previousPage" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "nextPage" })).toBeEnabled();
    screen.getByRole("button", { name: "previousPage" }).click();
    screen.getByRole("button", { name: "nextPage" }).click();
    expect(onPaginationChange).toHaveBeenNthCalledWith(1, 1, 20);
    expect(onPaginationChange).toHaveBeenNthCalledWith(2, 3, 20);
  });

  it("prefetches on sustained intent without navigating, and cancels quick passes and stale pages", async () => {
    vi.useFakeTimers();
    const prefetch = vi.fn();
    const navigate = vi.fn();
    const props = { page: 2, pageSize: 10, pageCount: 4, showPageSize: false, onPaginationChange: navigate, onPrefetchPage: prefetch };
    const view = render(<DataTablePagination {...props} />);
    const next = screen.getByRole("button", { name: "nextPage" });
    fireEvent.mouseEnter(next);
    await act(async () => { vi.advanceTimersByTime(100); });
    fireEvent.mouseLeave(next);
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(prefetch).not.toHaveBeenCalled();
    fireEvent.focus(next);
    await act(async () => { vi.advanceTimersByTime(149); });
    expect(prefetch).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(prefetch).toHaveBeenCalledExactlyOnceWith(3, 10);
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.blur(next);
    fireEvent.mouseEnter(next);
    await act(async () => { vi.advanceTimersByTime(100); });
    view.rerender(<DataTablePagination {...props} page={3} />);
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(prefetch).toHaveBeenCalledTimes(1);
    fireEvent.mouseEnter(next);
    view.unmount();
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(prefetch).toHaveBeenCalledTimes(1);
  });
});
