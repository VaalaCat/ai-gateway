import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DebouncedSearchInput } from "./debounced-search-input";

describe("DebouncedSearchInput", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("shows typing immediately and commits after 300ms", () => {
    const onCommit = vi.fn();
    render(<DebouncedSearchInput value="" onCommit={onCommit} placeholder="Search" />);

    fireEvent.change(screen.getByPlaceholderText("Search"), { target: { value: "model" } });
    expect(screen.getByDisplayValue("model")).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(299));
    expect(onCommit).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(onCommit).toHaveBeenCalledWith("model");
  });

  it("resets the draft when the controlled value changes", () => {
    const onCommit = vi.fn();
    const { rerender } = render(
      <DebouncedSearchInput value="old" onCommit={onCommit} placeholder="Search" />,
    );

    rerender(<DebouncedSearchInput value="new" onCommit={onCommit} placeholder="Search" />);

    expect(screen.getByDisplayValue("new")).toBeInTheDocument();
  });

  it("cancels a pending draft when an external value arrives", () => {
    const onCommit = vi.fn();
    const { rerender } = render(
      <DebouncedSearchInput value="" onCommit={onCommit} placeholder="Search" />,
    );
    fireEvent.change(screen.getByPlaceholderText("Search"), { target: { value: "stale" } });

    rerender(<DebouncedSearchInput value="server" onCommit={onCommit} placeholder="Search" />);
    act(() => vi.advanceTimersByTime(300));

    expect(onCommit).not.toHaveBeenCalled();
    expect(screen.getByDisplayValue("server")).toBeInTheDocument();
  });

  it("does not commit when the draft still equals the controlled value", () => {
    const onCommit = vi.fn();
    render(<DebouncedSearchInput value="same" onCommit={onCommit} placeholder="Search" />);

    act(() => vi.advanceTimersByTime(300));

    expect(onCommit).not.toHaveBeenCalled();
  });
});
