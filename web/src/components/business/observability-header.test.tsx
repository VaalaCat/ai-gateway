import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import type { DateRangePickerProps } from "./date-picker/date-range-picker";
import { ObservabilityHeader } from "./observability-header";
import { dateStrToTs } from "@/lib/utils/date-range";

const mocks = vi.hoisted(() => ({
  pickerProps: undefined as DateRangePickerProps | undefined,
  toastInfo: vi.fn(),
}));

vi.mock("next-intl", () => ({
  useTranslations: (namespace: string) => (key: string) => `${namespace}.${key}`,
}));

vi.mock("sonner", () => ({
  toast: { info: mocks.toastInfo },
}));

vi.mock("@/components/business/date-picker/date-range-picker", () => ({
  DateRangePicker: (props: DateRangePickerProps) => {
    mocks.pickerProps = props;
    return (
      <button
        type="button"
        data-testid="date-range"
        onClick={() =>
          props.onValueChange({ startDate: "2026-07-20", endDate: "2026-07-25" })
        }
      >
        {props.placeholder}
      </button>
    );
  },
}));

beforeEach(() => {
  mocks.pickerProps = undefined;
  mocks.toastInfo.mockReset();
});

it("delegates the title and subtitle to PageHeader while preserving observability controls", () => {
  render(
    <ObservabilityHeader
      title="Dashboard"
      subtitle="Overview"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
    />,
  );

  expect(screen.getByTestId("page-header")).toHaveTextContent("Dashboard");
  expect(screen.getByTestId("page-header")).toHaveTextContent("Overview");
  expect(screen.getByTestId("date-range")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
});

it("uses PageHeader above the independent observability controls", () => {
  render(
    <ObservabilityHeader
      title="Dashboard"
      subtitle="Overview"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
    />,
  );

  const header = screen.getByTestId("page-header");
  expect(header).toContainElement(screen.getByRole("heading", { level: 1, name: "Dashboard" }));
  expect(header).not.toContainElement(screen.getByTestId("date-range"));
  expect(screen.getByTestId("date-range").parentElement?.parentElement).toHaveClass(
    "lg:justify-end",
  );
});

it("can render only observability controls when the route already owns the PageHeader", () => {
  render(
    <ObservabilityHeader
      title="Dashboard"
      subtitle="Overview"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
      showPageHeader={false}
    />,
  );

  expect(screen.queryByRole("heading", { level: 1 })).not.toBeInTheDocument();
  expect(screen.getByTestId("date-range")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
});

it("emits one complete date range update and preserves granularity", async () => {
  const onRangeChange = vi.fn();
  render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={onRangeChange}
      onRefresh={() => {}}
    />,
  );

  await userEvent.click(screen.getByTestId("date-range"));

  expect(onRangeChange).toHaveBeenCalledOnce();
  expect(onRangeChange).toHaveBeenCalledWith({
    start: expect.any(Number),
    end: expect.any(Number),
    gran: "day",
  });
  const nextRange = onRangeChange.mock.calls[0][0];
  expect(nextRange.end).toBeGreaterThan(nextRange.start);
});

it("preserves a long atomic date range update and switches to day granularity", () => {
  const onRangeChange = vi.fn();
  render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "hour" }}
      onRangeChange={onRangeChange}
      onRefresh={() => {}}
    />,
  );

  mocks.pickerProps?.onValueChange({
    startDate: "2026-07-01",
    endDate: "2026-07-25",
  });

  const nextRange = onRangeChange.mock.calls[0][0];
  expect(nextRange).toEqual({
    start: dateStrToTs("2026-07-01", false),
    end: dateStrToTs("2026-07-25", true),
    gran: "day",
  });
  expect(mocks.toastInfo).toHaveBeenCalledWith("monitoring.range.longRangeUsesDay");
});

it("keeps a long range unchanged and uses day granularity when hour is selected", async () => {
  const onRangeChange = vi.fn();
  const range = {
    start: dateStrToTs("2026-07-01", false),
    end: dateStrToTs("2026-07-25", true),
    gran: "day" as const,
  };
  render(
    <ObservabilityHeader
      title="Dashboard"
      range={range}
      onRangeChange={onRangeChange}
      onRefresh={() => {}}
    />,
  );

  await userEvent.click(screen.getByRole("tab", { name: "monitoring.range.hour" }));

  expect(onRangeChange).toHaveBeenCalledWith(range);
  expect(mocks.toastInfo).toHaveBeenCalledWith("monitoring.range.longRangeUsesDay");
});

it("switches a range within seven days to hour granularity", async () => {
  const onRangeChange = vi.fn();
  const range = {
    start: dateStrToTs("2026-07-20", false),
    end: dateStrToTs("2026-07-25", true),
    gran: "day" as const,
  };
  render(
    <ObservabilityHeader
      title="Dashboard"
      range={range}
      onRangeChange={onRangeChange}
      onRefresh={() => {}}
    />,
  );

  await userEvent.click(screen.getByRole("tab", { name: "monitoring.range.hour" }));

  expect(onRangeChange).toHaveBeenCalledWith({ ...range, gran: "hour" });
});

it("uses compact 32px controls and the billing date range placeholder", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
    />,
  );

  expect(mocks.pickerProps).toMatchObject({
    size: "sm",
    placeholder: "billing.dateRange",
    className: "col-span-2 w-full [&_[data-slot=date-range-trigger]]:h-9 sm:w-auto sm:[&_[data-slot=date-range-trigger]]:h-8",
  });
  expect(container.querySelector('[data-slot="tabs-list"]')).toHaveClass("!h-9", "sm:!h-8");
  const refresh = screen.getByRole("button", { name: "Refresh" });
  expect(refresh).toHaveAttribute("data-size", "icon-sm");
  expect(refresh).toHaveClass("size-9", "sm:size-8");
  expect(refresh.parentElement).toHaveAttribute("data-slot", "header-actions");
});

it("forwards an optional date range limit to the picker", () => {
  render(
    <ObservabilityHeader
      title="Logs"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
      maxDays={7}
    />,
  );

  expect(mocks.pickerProps).toMatchObject({ maxDays: 7 });
});

it("keeps date range and refresh on one row when granularity is hidden", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Logs"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
      showGranularity={false}
    />,
  );

  expect(mocks.pickerProps?.className).toContain("col-start-1 row-start-1");
  expect(mocks.pickerProps?.className).not.toContain("col-span-2");
  expect(container.querySelector('[data-slot="header-actions"]')).toHaveClass(
    "col-start-2",
    "row-start-1",
  );
});

it("does not render a page scope rail without scope controls", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
    />,
  );

  expect(container.querySelector('[data-slot="page-scope-rail"]')).not.toBeInTheDocument();
});

it("renders the page scope label and controls in a compact rail", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
      scopeLabel="Scope"
      scopeControls={<button type="button">Model</button>}
    />,
  );

  const rail = container.querySelector('[data-slot="page-scope-rail"]');
  const controls = screen.getByRole("button", { name: "Model" }).parentElement;
  expect(rail).toHaveTextContent("Scope");
  expect(rail).toContainElement(screen.getByRole("button", { name: "Model" }));
  expect(rail).toHaveClass("gap-2");
  expect(controls).toHaveClass("grid", "grid-cols-2", "sm:flex", "sm:flex-wrap");
  expect(rail?.querySelector('[data-slot="separator"]')).toBeInTheDocument();
});

it("puts optional header actions in PageHeader while preserving the refresh action slot", () => {
  const { container } = render(
    <ObservabilityHeader
      title="Dashboard"
      range={{ start: 100, end: 200, gran: "day" }}
      onRangeChange={() => {}}
      onRefresh={() => {}}
      headerActions={<button type="button">More actions</button>}
    />,
  );

  const refresh = screen.getByRole("button", { name: "Refresh" });
  const actions = screen.getByRole("button", { name: "More actions" });
  expect(actions.parentElement).toBe(screen.getByTestId("page-header-actions"));
  expect(refresh.parentElement).toHaveAttribute("data-slot", "header-actions");
  expect(container.querySelector('[data-slot="header-actions-row"]')).not.toBeInTheDocument();
});
