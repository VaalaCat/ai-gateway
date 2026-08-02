import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Tracker } from "./tracker";

describe("Tracker", () => {
  it("keeps compact blocks at fixed hourly geometry without growing", () => {
    const { container } = render(<Tracker
      layout="compact"
      data={[
        { key: "first", ariaLabel: "First hour" },
        { key: "second", ariaLabel: "Second hour" },
      ]}
    />);

    const root = container.querySelector("[data-slot='tracker']");
    const blocks = container.querySelectorAll("[data-slot='tracker-block']");
    expect(root).toHaveClass("w-fit", "gap-0.5");
    expect(blocks).toHaveLength(2);
    for (const block of blocks) {
      expect(block).toHaveClass("h-4", "w-[5px]", "shrink-0", "rounded-[1px]");
      expect(block).not.toHaveClass("flex-1");
    }
  });

  it("renders accessible blocks with Tremor edge geometry", async () => {
    const user = userEvent.setup();
    const { container } = render(<Tracker data={[
      { key: "ok", color: "bg-emerald-600", tooltip: "Operational", ariaLabel: "Operational", state: "ok" },
      { key: "warn", color: "bg-amber-500", tooltip: "Degraded", ariaLabel: "Degraded", state: "warn" },
    ]} />);

    const blocks = screen.getAllByRole("img");
    const wrappers = container.querySelectorAll("[data-slot='tracker-block']");
    const root = container.querySelector("[data-slot='tracker']");
    expect(blocks).toHaveLength(2);
    expect(root).toHaveClass("h-8", "w-full");
    expect(root).not.toHaveClass("w-fit", "gap-0.5");
    expect(blocks[0]).toHaveAttribute("data-state", "ok");
    expect(wrappers).toHaveLength(2);
    expect(wrappers[0]).toHaveClass("first:rounded-l-[4px]", "first:pl-0");
    expect(wrappers[1]).toHaveClass("last:rounded-r-[4px]", "last:pr-0");
    expect(blocks[0]).not.toHaveClass("first:pl-0", "last:pr-0");

    await user.tab();
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Operational");
  });

  it("renders an empty tracker without fabricated blocks", () => {
    render(<Tracker data={[]} aria-label="Empty history" />);

    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });

  it("keeps one block full width at the boundary", () => {
    const { container } = render(<Tracker data={[{ key: "only", ariaLabel: "Unknown" }]} />);

    expect(screen.getByRole("img", { name: "Unknown" })).toBeInTheDocument();
    expect(container.querySelector("[data-slot='tracker-block']"))
      .toHaveClass(
        "first:rounded-l-[4px]",
        "first:pl-0",
        "last:rounded-r-[4px]",
        "last:pr-0",
      );
  });

  it("uses its aria label without creating an empty tooltip", () => {
    render(<Tracker data={[{ key: "unknown", ariaLabel: "Unknown" }]} />);

    expect(screen.getByRole("img", { name: "Unknown" })).toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });

  it("uses caller-provided progress and inset focus styles inside clipped blocks", () => {
    render(<Tracker data={[{
      key: "collecting",
      ariaLabel: "Collecting",
      inProgress: true,
      indicatorClassName: "ring-amber-500/80 ring-inset",
    }]} />);

    expect(screen.getByRole("img", { name: "Collecting" }))
      .toHaveClass("ring-amber-500/80", "ring-inset", "focus-visible:ring-inset");
    expect(screen.getByRole("img", { name: "Collecting" }))
      .not.toHaveClass("focus-visible:ring-offset-1");
  });
});
