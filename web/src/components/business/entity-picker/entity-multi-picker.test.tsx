import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { EntityMultiPicker } from "./entity-multi-picker";

const { useEntityOptions } = vi.hoisted(() => ({ useEntityOptions: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("./use-entity-options", () => ({ useEntityOptions }));
vi.mock("./registry", () => ({
  ENTITY_ADAPTERS: { channel: { useOne: vi.fn() } },
}));
vi.mock("@/components/business/entity-label", () => ({
  EntityLabel: ({ id, scope }: { id: string; scope?: string }) => (
    <span data-testid={`label-${id}`} data-scope={scope} />
  ),
}));

describe("EntityMultiPicker admin scope", () => {
  beforeEach(() => {
    useEntityOptions.mockReset();
    useEntityOptions.mockReturnValue({
      search: "",
      setSearch: vi.fn(),
      items: [],
      isLoading: false,
      getValue: vi.fn(),
      renderItem: vi.fn(),
    });
  });

  it("uses self when scope is omitted", () => {
    render(<EntityMultiPicker entity="channel" value={[]} onChange={vi.fn()} />);

    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "self",
      pageSize: 50,
    });
  });

  it("passes an explicit all scope through to entity queries", () => {
    render(<EntityMultiPicker entity="channel" scope="all" value={[]} onChange={vi.fn()} />);

    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "all",
      pageSize: 50,
    });
  });

  it("preserves an explicit scope when the popover opens", async () => {
    const user = userEvent.setup();
    render(<EntityMultiPicker entity="channel" scope="all" value={[]} onChange={vi.fn()} />);

    await user.click(screen.getByRole("combobox"));

    expect(useEntityOptions).toHaveBeenCalledTimes(2);
    for (const [, options] of useEntityOptions.mock.calls) {
      expect(options).toEqual({ scope: "all", pageSize: 50 });
    }
  });

  it("uses the explicit scope when resolving selected labels", () => {
    render(<EntityMultiPicker entity="channel" scope="all" value={["7"]} onChange={vi.fn()} />);

    expect(screen.getByTestId("label-7")).toHaveAttribute("data-scope", "all");
  });
});
