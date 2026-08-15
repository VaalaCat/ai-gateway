import { fireEvent, render, screen } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { SearchableSelect } from "./searchable-select";

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => undefined;
});

describe("SearchableSelect", () => {
  it("keeps its explicit accessible name when the selected label changes", () => {
    const props = {
      ariaLabel: "API Service",
      onChange: () => undefined,
      placeholder: "Choose a Service",
      searchPlaceholder: "Search Services",
      items: [
        { value: "7", label: "Weather" },
        { value: "8", label: "Maps" },
      ],
    };
    const view = render(<SearchableSelect {...props} value="" />);

    expect(screen.getByRole("combobox", { name: "API Service" })).toHaveTextContent("Choose a Service");

    view.rerender(<SearchableSelect {...props} value="8" />);
    expect(screen.getByRole("combobox", { name: "API Service" })).toHaveTextContent("Maps");
  });

  it("commits remote search once after the configured debounce without filtering server candidates locally", async () => {
    const onCommit = vi.fn();
    render(
      <SearchableSelect
        value=""
        onChange={() => undefined}
        ariaLabel="API Service"
        placeholder="Choose a Service"
        searchPlaceholder="Search Services"
        emptyText="No Services"
        items={[{ value: "7", label: "Weather" }]}
        remoteSearch={{ value: "", onCommit, debounceMs: 300 }}
      />,
    );

    fireEvent.click(screen.getByRole("combobox", { name: "API Service" }));
    fireEvent.change(screen.getByPlaceholderText("Search Services"), {
      target: { value: "Maps" },
    });

    expect(screen.getByRole("option", { name: "Weather" })).toBeInTheDocument();
    expect(onCommit).not.toHaveBeenCalled();
    await new Promise((resolve) => setTimeout(resolve, 320));
    expect(onCommit).toHaveBeenCalledWith("Maps");
  });

  it("cancels a stale remote commit when the external search value resets", async () => {
    const onCommit = vi.fn();
    const props = {
      value: "",
      onChange: () => undefined,
      ariaLabel: "API Route",
      placeholder: "Choose a Route",
      searchPlaceholder: "Search Routes",
      items: [{ value: "9", label: "forecast" }],
      remoteSearch: { value: "old", onCommit, debounceMs: 300 },
    };
    const view = render(<SearchableSelect {...props} />);
    fireEvent.click(screen.getByRole("combobox", { name: "API Route" }));
    fireEvent.change(screen.getByPlaceholderText("Search Routes"), {
      target: { value: "radar" },
    });

    view.rerender(
      <SearchableSelect
        {...props}
        remoteSearch={{ ...props.remoteSearch, value: "" }}
      />,
    );
    await new Promise((resolve) => setTimeout(resolve, 320));

    expect(onCommit).not.toHaveBeenCalledWith("radar");
    expect(screen.getByPlaceholderText("Search Routes")).toHaveValue("");
  });

  it("keeps the selected server label visible when it is outside the current candidate page", () => {
    render(
      <SearchableSelect
        value="9"
        onChange={() => undefined}
        ariaLabel="API Route"
        placeholder="Choose a Route"
        searchPlaceholder="Search Routes"
        items={[]}
        selectedLabel="forecast"
        remoteSearch={{ value: "radar", onCommit: () => undefined }}
      />,
    );

    expect(screen.getByRole("combobox", { name: "API Route" })).toHaveTextContent("forecast");
  });
});
