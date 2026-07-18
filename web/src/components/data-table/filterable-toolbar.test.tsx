import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { FilterableToolbar } from "./filterable-toolbar";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

it("resets a text draft immediately and never emits the old debounce value", () => {
  const onChange = vi.fn();
  const spec = { search: { kind: "text" as const, placeholder: "search", debounceMs: 100 } };
  const { rerender } = render(<FilterableToolbar spec={spec} value={{ search: "old" }} onChange={onChange} />);

  fireEvent.change(screen.getByPlaceholderText("search"), { target: { value: "draft" } });
  rerender(<FilterableToolbar spec={spec} value={{ search: "server" }} onChange={onChange} />);

  expect(screen.getByPlaceholderText("search")).toHaveValue("server");
  vi.advanceTimersByTime(200);
  expect(onChange).not.toHaveBeenCalledWith({ search: "draft" });
});

it("does not emit a stale baseline when typing after an authoritative reset", () => {
  const onChange = vi.fn();
  const spec = { search: { kind: "text" as const, placeholder: "search", debounceMs: 100 } };
  const { rerender } = render(<FilterableToolbar spec={spec} value={{ search: "old" }} onChange={onChange} />);

  rerender(<FilterableToolbar spec={spec} value={{ search: "server" }} onChange={onChange} />);
  fireEvent.change(screen.getByPlaceholderText("search"), { target: { value: "fresh" } });

  expect(onChange).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(100));
  expect(onChange).toHaveBeenCalledOnce();
  expect(onChange).toHaveBeenCalledWith({ search: "fresh" });
});
