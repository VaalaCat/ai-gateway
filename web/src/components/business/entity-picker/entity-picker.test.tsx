import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { EntityPicker } from "./entity-picker";

type EntityOptionsMock = {
  search: string;
  setSearch: (search: string) => void;
  items: unknown[];
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => unknown;
  getValue: (item: unknown) => string;
  renderItem: (item: unknown) => ReactNode;
};

type EntityOneMock = {
	data: unknown;
	isError?: boolean;
	error?: Error | null;
	refetch?: () => unknown;
};

const mocks = vi.hoisted(() => ({
  refetch: vi.fn(),
  useOne: vi.fn((): EntityOneMock => ({ data: null })),
  options: {
    search: "",
    setSearch: () => {},
    items: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    getValue: () => "",
    renderItem: () => null,
  } as EntityOptionsMock,
}));
const useEntityOptions = vi.hoisted(() => vi.fn(() => mocks.options));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: false }) }));
vi.mock("@/components/business/admin-scope-toggle", () => ({ AdminScopeToggle: () => null }));
vi.mock("./use-entity-options", () => ({
  useEntityOptions,
}));
vi.mock("./registry", () => ({
  ENTITY_ADAPTERS: {
    token: {
      useOne: mocks.useOne,
      getLabel: () => "",
      supportsAdminScope: false,
    },
  },
}));

beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn();
  mocks.refetch.mockReset();
  mocks.useOne.mockClear();
  mocks.options = {
    search: "",
    setSearch: () => {},
    items: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    getValue: () => "",
    renderItem: () => null,
  };
});

it("renders default size with h-full trigger (no h-8)", () => {
  render(<EntityPicker entity="token" value="" onChange={() => {}} />);
  const btn = screen.getByRole("combobox");
  expect(btn.className).toContain("h-full");
  expect(btn.className).not.toContain("h-8");
  expect(btn.className).toContain("min-h-11");
});

it("keeps the default clear action at a 44 by 44 touch target", () => {
  render(<EntityPicker entity="token" value="9" onChange={() => {}} />);
  expect(screen.getByRole("button", { name: "clear" })).toHaveClass("min-h-11", "min-w-11");
});

it("renders sm size with h-8 trigger", () => {
  render(<EntityPicker entity="token" value="" onChange={() => {}} size="sm" />);
  const btn = screen.getByRole("combobox");
  expect(btn.className).toContain("h-8");
  expect(btn.className).not.toContain("h-full");
});

it("renders xs size with h-7 trigger", () => {
  render(<EntityPicker entity="token" value="" onChange={() => {}} size="xs" />);
  const btn = screen.getByRole("combobox");
  expect(btn.className).toContain("h-7");
  expect(btn.className).not.toContain("h-full");
});

it("shows placeholder fallback text when no value selected", () => {
  render(<EntityPicker entity="token" value="" onChange={() => {}} placeholder="pick token" size="sm" />);
  expect(screen.getByRole("combobox")).toHaveTextContent("pick token");
});

it("forwards a field id to the combobox trigger", () => {
  render(
    <EntityPicker
      id="catalog-token"
      entity="token"
      value=""
      onChange={() => {}}
    />,
  );

  expect(screen.getByRole("combobox")).toHaveAttribute("id", "catalog-token");
});

it("passes an explicit token owner to both list and selected-value contracts", () => {
  render(
    <EntityPicker entity="token" value="9" onChange={() => {}} ownerUserId={42} />,
  );

  expect(useEntityOptions).toHaveBeenLastCalledWith(
    expect.anything(),
    expect.objectContaining({ ownerUserId: 42 }),
  );
  expect(mocks.useOne).toHaveBeenLastCalledWith(
    "9",
    expect.objectContaining({ ownerUserId: 42 }),
  );
});

it("passes apiServiceId to both list and selected-value adapter contracts", () => {
  render(
    <EntityPicker entity="token" value="9" onChange={() => {}} apiServiceId={7} />,
  );

  expect(useEntityOptions).toHaveBeenLastCalledWith(
    expect.anything(),
    expect.objectContaining({ apiServiceId: 7 }),
  );
  expect(mocks.useOne).toHaveBeenLastCalledWith(
    "9",
    expect.objectContaining({ apiServiceId: 7 }),
  );
});

it("passes apiRouteId to both list and selected-value adapter contracts", () => {
	render(
		<EntityPicker entity="token" value="9" onChange={() => {}} apiServiceId={7} apiRouteId={11} />,
	);

	expect(useEntityOptions).toHaveBeenLastCalledWith(
		expect.anything(),
		expect.objectContaining({ apiRouteId: 11 }),
	);
	expect(mocks.useOne).toHaveBeenLastCalledWith(
		"9",
		expect.objectContaining({ apiRouteId: 11 }),
	);
});

it("shows a retry-only error state instead of an empty result state", () => {
  mocks.options = {
    ...mocks.options,
    items: [],
    isError: true,
    error: new Error("request failed"),
    refetch: mocks.refetch,
  };
  render(<EntityPicker entity="token" value="" onChange={() => {}} />);

  fireEvent.click(screen.getByRole("combobox"));

  expect(document.querySelector('[data-slot="entity-picker"]')).toHaveAttribute(
    "data-state",
    "open",
  );
  const error = document.querySelector('[data-slot="entity-picker-error"]');
  const retry = document.querySelector('[data-slot="entity-picker-retry"]');
  expect(error).toHaveAttribute("data-state", "error");
  expect(error).toHaveTextContent("loadFailed");
  expect(retry).toHaveAttribute("data-state", "error");
  expect(retry).toHaveTextContent("retry");
  expect(screen.queryByText("noResults")).not.toBeInTheDocument();
  expect(screen.queryByText("request failed")).not.toBeInTheDocument();

  fireEvent.click(retry as HTMLButtonElement);
  expect(mocks.refetch).toHaveBeenCalledOnce();
});

it("closes an open picker and prevents stale option selection when disabled", () => {
  const onChange = vi.fn();
  mocks.options = {
    ...mocks.options,
    items: [{ id: "stale" }],
    getValue: () => "stale",
    renderItem: () => "stale option",
  };
  const { rerender } = render(
    <EntityPicker entity="token" value="" onChange={onChange} />,
  );

  fireEvent.click(screen.getByRole("combobox"));
  const staleOption = screen.getByRole("option", { name: "stale option" });

  rerender(<EntityPicker entity="token" value="" onChange={onChange} disabled />);

  expect(document.querySelector('[data-slot="entity-picker"]')).toHaveAttribute(
    "data-state",
    "disabled",
  );
  expect(document.querySelector('[data-slot="popover-trigger"]')).toHaveAttribute(
    "aria-expanded",
    "false",
  );
  expect(screen.queryByRole("option", { name: "stale option" })).not.toBeInTheDocument();

  fireEvent.click(staleOption);
  expect(onChange).not.toHaveBeenCalled();

  rerender(<EntityPicker entity="token" value="" onChange={onChange} />);
  expect(document.querySelector('[data-slot="entity-picker"]')).toHaveAttribute(
    "data-state",
    "closed",
  );
});

it.each([
  ["empty", [], "noResults"],
  ["data", [{ id: "visible" }], "visible option"],
] as const)(
  "keeps a %s candidate list available when selected-value lookup fails",
  (_listState, items, expectedText) => {
    mocks.useOne.mockReturnValue({
      data: null,
      isError: true,
      error: new Error("selected lookup failed"),
		refetch: mocks.refetch,
    });
    mocks.options = {
      ...mocks.options,
      items: [...items],
      isError: false,
      error: null,
      refetch: mocks.refetch,
      getValue: () => "visible",
      renderItem: () => "visible option",
    };
    render(<EntityPicker entity="token" value="selected" onChange={() => {}} />);

    fireEvent.click(screen.getByRole("combobox"));

		expect(screen.getByText(expectedText)).toBeInTheDocument();
    expect(screen.getByText("loadFailed")).toBeInTheDocument();
    expect(screen.queryByText("selected lookup failed")).not.toBeInTheDocument();
    expect(document.querySelector('[data-slot="entity-picker-selected-error"]')).toHaveAttribute("data-state", "error");
  },
);
