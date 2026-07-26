import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { FilterableToolbar } from "./filterable-toolbar";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: true }) }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({
    placeholder,
    value,
    onChange,
  }: {
    placeholder?: string;
    value?: string;
    onChange: (value: string) => void;
  }) => (
    <button
      type="button"
      role="combobox"
      aria-controls="entity-options"
      aria-expanded="false"
      data-value={value}
      onClick={() => onChange("picked")}
    >
      {placeholder}
    </button>
  ),
}));

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

it("wraps each filter in a labeled FilterField, label falls back to placeholder", () => {
  const spec = {
    search: { kind: "text" as const, placeholder: "searchPh" },
    status: {
      kind: "enum" as const,
      label: "statusLabel",
      options: [{ value: "1", label: "on" }],
      placeholder: "allStatus",
    },
  };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.getByText("searchPh")).toBeInTheDocument(); // label 元素(≠input placeholder)
  expect(screen.getByText("statusLabel")).toBeInTheDocument(); // def.label 优先
});

it("renders compact sm controls in the toolbar", () => {
  const spec = {
    search: { kind: "text" as const, placeholder: "searchPh" },
    status: { kind: "enum" as const, options: [{ value: "1", label: "on" }], placeholder: "st" },
  };
  const { container } = render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(container.querySelector('[data-slot="select-trigger"]')?.getAttribute("data-size")).toBe("sm");
  expect(screen.getByPlaceholderText("searchPh").className).toContain("h-8");
});

it("splits time kind into two labeled date fields", () => {
  const spec = { time: { kind: "time" as const } };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.getByText("startDate")).toBeInTheDocument();
  expect(screen.getByText("endDate")).toBeInTheDocument();
});

it("does not duplicate enum placeholder text between label and trigger", () => {
  const spec = {
    status: { kind: "enum" as const, options: [{ value: "1", label: "on" }], placeholder: "statusPh" },
  };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.getAllByText("statusPh")).toHaveLength(1); // 仅标签
  expect(screen.getByText("all")).toBeInTheDocument(); // 触发器显示「全部」
});

it("picker label falls back to entity noun and trigger shows the all placeholder", () => {
  const spec = { token_id: { kind: "picker" as const, entity: "token" as const } };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.getByText("label.token")).toBeInTheDocument(); // 名词标签 key 回显
  expect(screen.getByRole("combobox")).toHaveTextContent("all");
});

it("collapses advanced filters into a popover, opened on click", () => {
  const spec = {
    search: { kind: "text" as const, placeholder: "searchPh" },
    scope: {
      kind: "enum" as const,
      advanced: true,
      options: [{ value: "1", label: "opt1" }],
      placeholder: "scopePh",
    },
  };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.getByText("searchPh")).toBeInTheDocument();
  expect(screen.getByPlaceholderText("searchPh")).toBeInTheDocument();
  expect(screen.queryByText("scopePh")).not.toBeInTheDocument(); // 未展开不渲染
  fireEvent.click(screen.getByRole("button", { name: /filters/ }));
  expect(screen.getByText("scopePh")).toBeInTheDocument(); // Popover 内 FilterField label
});

it("renders an advanced picker in the outer popover and wires its value change", () => {
  const onChange = vi.fn();
  const spec = {
    token_id: {
      kind: "picker" as const,
      entity: "token" as const,
      advanced: true,
      label: "advancedToken",
    },
  };
  render(
    <FilterableToolbar
      spec={spec}
      value={{ token_id: "current" }}
      onChange={onChange}
    />,
  );

  expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /filters/ }));

  const popover = document.querySelector('[data-slot="popover-content"]');
  const picker = screen.getByRole("combobox");
  expect(popover).toContainElement(picker);
  expect(picker).toHaveAttribute("data-value", "current");
  fireEvent.click(picker);
  expect(onChange).toHaveBeenCalledWith({ token_id: "picked" });
});

it("makes both advanced time fields and date pickers full width", () => {
  const spec = { time: { kind: "time" as const, advanced: true } };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);

  fireEvent.click(screen.getByRole("button", { name: /filters/ }));

  for (const label of ["startDate", "endDate"]) {
    const field = screen.getByText(label).parentElement;
    expect(field).toHaveClass("w-full");
    const datePicker = field?.querySelector(":scope > div");
    expect(datePicker).toHaveClass("w-full");
    expect(datePicker).toHaveClass("sm:[&_[data-slot=popover-trigger]]:w-full");
  }
  expect(document.querySelector('[data-slot="popover-content"]')).toHaveClass(
    "flex",
    "flex-col",
    "gap-3",
  );
});

it("shows active advanced filter count as a badge, hidden at zero", () => {
  const spec = {
    a: { kind: "text" as const, advanced: true, placeholder: "aPh" },
    b: {
      kind: "enum" as const,
      advanced: true,
      options: [{ value: "1", label: "x" }],
      placeholder: "bPh",
    },
  };
  const { rerender } = render(
    <FilterableToolbar spec={spec} value={{}} onChange={() => {}} />,
  );
  const trigger = screen.getByRole("button", { name: /filters/ });
  expect(trigger.textContent).not.toMatch(/\d/); // 0 激活无 Badge
  rerender(<FilterableToolbar spec={spec} value={{ a: "x", b: "1" }} onChange={() => {}} />);
  expect(screen.getByRole("button", { name: /filters/ }).textContent).toContain("2");
});

it.each([
  [undefined, false],
  ["", false],
  [0, false],
  ["0", true],
  ["active", true],
  [1, true],
] as const)("treats advanced value %s active=%s consistently with URL filter state", (filterValue, active) => {
  const spec = { advanced: { kind: "text" as const, advanced: true } };
  render(
    <FilterableToolbar
      spec={spec}
      value={{ advanced: filterValue }}
      onChange={() => {}}
    />,
  );

  const trigger = screen.getByRole("button", { name: /filters/ });
  expect(trigger.textContent?.includes("1")).toBe(active);
});

it("excludes invisible advanced fields from popover and badge count", () => {
  const spec = {
    hiddenOne: {
      kind: "text" as const,
      advanced: true,
      placeholder: "hiddenPh",
      visible: () => false,
    },
    shown: { kind: "text" as const, advanced: true, placeholder: "shownPh" },
  };
  render(
    <FilterableToolbar spec={spec} value={{ hiddenOne: "x" }} onChange={() => {}} />,
  );
  const trigger = screen.getByRole("button", { name: /filters/ });
  expect(trigger.textContent).not.toMatch(/\d/); // 不可见字段的值不计数
  fireEvent.click(trigger);
  expect(screen.queryByText("hiddenPh")).not.toBeInTheDocument();
  expect(screen.getByText("shownPh")).toBeInTheDocument();
});

it("renders no popover trigger when spec has no advanced fields", () => {
  const spec = { search: { kind: "text" as const, placeholder: "searchPh" } };
  render(<FilterableToolbar spec={spec} value={{}} onChange={() => {}} />);
  expect(screen.queryByRole("button", { name: /filters/ })).not.toBeInTheDocument();
});

it("puts filters on their own desktop row with tighter spacing and right-aligns actions", () => {
  const spec = { search: { kind: "text" as const, placeholder: "searchPh" } };
  const { container } = render(
    <FilterableToolbar
      spec={spec}
      value={{}}
      onChange={() => {}}
      filtersOnOwnRow
      secondaryContent={<span>refreshControl</span>}
    />,
  );

  expect(container.querySelector('[data-slot="toolbar-filters"]')).toHaveClass(
    "sm:gap-2",
    "md:basis-full",
  );
  expect(container.querySelector('[data-slot="toolbar-filters"]')).not.toHaveClass("md:flex-1");
  expect(container.querySelector('[data-slot="toolbar-actions"]')).toHaveClass("md:ml-auto");
});

it("keeps the default desktop layout on one shared row", () => {
  const spec = { search: { kind: "text" as const, placeholder: "searchPh" } };
  const { container } = render(
    <FilterableToolbar
      spec={spec}
      value={{}}
      onChange={() => {}}
      secondaryContent={<span>refreshControl</span>}
    />,
  );

  expect(container.querySelector('[data-slot="toolbar-filters"]')).toHaveClass(
    "sm:gap-3",
    "md:flex-1",
  );
  expect(container.querySelector('[data-slot="toolbar-filters"]')).not.toHaveClass("md:basis-full");
  expect(container.querySelector('[data-slot="toolbar-actions"]')).not.toHaveClass("md:ml-auto");
});

it("omits the empty filters container and keeps actions as the only root child", () => {
  const { container } = render(
    <FilterableToolbar
      spec={{}}
      value={{}}
      onChange={() => {}}
      filtersOnOwnRow
      secondaryContent={<span>refreshControl</span>}
      primaryAction={<button type="button">createAction</button>}
    />,
  );

  const root = container.firstElementChild;
  const actions = container.querySelector('[data-slot="toolbar-actions"]');
  expect(container.querySelector('[data-slot="toolbar-filters"]')).not.toBeInTheDocument();
  expect(root?.children).toHaveLength(1);
  expect(actions?.parentElement).toBe(root);
  expect(actions).toHaveClass("md:ml-auto");
  expect(actions).toContainElement(screen.getByText("refreshControl"));
  expect(actions).toContainElement(screen.getByRole("button", { name: "createAction" }));
});
