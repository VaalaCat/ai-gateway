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
  EntityLabel: ({ id, scope, apiServiceId }: { id: string; scope?: string; apiServiceId?: number }) => (
    <span data-testid={`label-${id}`} data-scope={scope} data-api-service-id={apiServiceId} />
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
      isError: false,
      refetch: vi.fn(),
      getValue: vi.fn(),
      renderItem: vi.fn(),
    });
  });

  it("uses self when scope is omitted", () => {
    render(<EntityMultiPicker entity="channel" value={[]} onChange={vi.fn()} />);

    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "self",
      pageSize: 50,
      enabled: false,
    });
  });

  it("passes an explicit all scope through to entity queries", () => {
    render(<EntityMultiPicker entity="channel" scope="all" value={[]} onChange={vi.fn()} />);

    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "all",
      pageSize: 50,
      enabled: false,
    });
  });

  it("preserves an explicit scope when the popover opens", async () => {
    const user = userEvent.setup();
    render(<EntityMultiPicker entity="channel" scope="all" value={[]} onChange={vi.fn()} />);

    await user.click(screen.getByRole("combobox"));

    expect(useEntityOptions).toHaveBeenCalledTimes(2);
    for (const [, options] of useEntityOptions.mock.calls) {
      expect(options).toMatchObject({ scope: "all", pageSize: 50 });
    }
  });

  it("keeps the popover closed after a disabled picker is enabled again", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(
      <EntityMultiPicker entity="channel" value={[]} onChange={onChange} />,
    );

    await user.click(screen.getByRole("combobox"));
    expect(screen.getAllByRole("combobox")[0]).toHaveAttribute("aria-expanded", "true");

    rerender(
      <EntityMultiPicker entity="channel" value={[]} onChange={onChange} disabled />,
    );
    rerender(
      <EntityMultiPicker entity="channel" value={[]} onChange={onChange} />,
    );

    expect(screen.getByRole("combobox")).toHaveAttribute("aria-expanded", "false");
    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "self",
      pageSize: 50,
      enabled: false,
    });
  });

  it("uses the explicit scope when resolving selected labels", () => {
    render(<EntityMultiPicker entity="channel" scope="all" value={["7"]} onChange={vi.fn()} />);

    expect(screen.getByTestId("label-7")).toHaveAttribute("data-scope", "all");
  });

  it("passes an API service parent to candidate and selected-label reads", () => {
    render(
      <EntityMultiPicker
        entity="channel"
        apiServiceId={7}
        value={["9"]}
        onChange={vi.fn()}
      />,
    );

    expect(useEntityOptions).toHaveBeenLastCalledWith(expect.anything(), {
      scope: "self",
      pageSize: 50,
      apiServiceId: 7,
      enabled: false,
    });
    expect(screen.getByTestId("label-9")).toHaveAttribute("data-api-service-id", "7");
  });

  it("keeps an unavailable candidate request visible as retryable error instead of empty", async () => {
    const user = userEvent.setup();
    useEntityOptions.mockReturnValue({
      search: "",
      setSearch: vi.fn(),
      items: [],
      isLoading: false,
      isError: true,
      refetch: vi.fn(),
      getValue: vi.fn(),
      renderItem: vi.fn(),
    });
    render(<EntityMultiPicker entity="channel" value={[]} onChange={vi.fn()} />);

    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText("loadFailed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "retry" })).toBeInTheDocument();
  });
});
