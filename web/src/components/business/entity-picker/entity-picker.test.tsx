import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { EntityPicker } from "./entity-picker";

const useEntityOptions = vi.hoisted(() => vi.fn(() => ({
  search: "",
  setSearch: () => {},
  items: [],
  isLoading: false,
  getValue: () => "",
  renderItem: () => null,
})));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: false }) }));
vi.mock("@/components/business/admin-scope-toggle", () => ({ AdminScopeToggle: () => null }));
vi.mock("./use-entity-options", () => ({
  useEntityOptions,
}));
vi.mock("./registry", () => ({
  ENTITY_ADAPTERS: {
    token: {
      useOne: () => ({ data: null }),
      getLabel: () => "",
      supportsAdminScope: false,
    },
  },
}));

it("renders default size with h-full trigger (no h-8)", () => {
  render(<EntityPicker entity="token" value="" onChange={() => {}} />);
  const btn = screen.getByRole("combobox");
  expect(btn.className).toContain("h-full");
  expect(btn.className).not.toContain("h-8");
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

it("passes an explicit token owner to the adapter list contract", () => {
  render(
    <EntityPicker entity="token" value="" onChange={() => {}} ownerUserId={42} />,
  );

  expect(useEntityOptions).toHaveBeenLastCalledWith(
    expect.anything(),
    expect.objectContaining({ ownerUserId: 42 }),
  );
});
