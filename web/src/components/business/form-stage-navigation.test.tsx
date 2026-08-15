import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FormStageNavigation, FormStageNavigationMobile } from "./form-stage-navigation";

const stages = [
  { id: "entry", title: "Public entry", configured: true },
  { id: "target", title: "Target", configured: false },
] as const;
const statusLabels = { configuredLabel: "Configured", unconfiguredLabel: "Not configured" };

describe("FormStageNavigation", () => {
  it("exposes active and configured state through accessible stage controls", () => {
    render(<FormStageNavigation stages={stages} activeId="entry" onSelect={vi.fn()} {...statusLabels} />);

    expect(screen.getByRole("button", { name: "Public entry" })).toHaveAttribute("aria-current", "step");
    expect(screen.getByRole("button", { name: "Public entry" })).toHaveAccessibleDescription("Configured");
    expect(screen.getByRole("button", { name: "Target" })).toHaveAccessibleDescription("Not configured");
  });

  it("selects a desktop stage through keyboard activation", async () => {
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(<FormStageNavigation stages={stages} activeId="entry" onSelect={onSelect} {...statusLabels} />);

    await user.tab();
    await user.keyboard("{ArrowDown}{Enter}");

    expect(onSelect).toHaveBeenCalledWith("target");
  });

  it("supports reverse, Home, and End stage keyboard movement", async () => {
    const user = userEvent.setup();
    render(<FormStageNavigation stages={stages} activeId="target" onSelect={vi.fn()} {...statusLabels} />);
    const entry = screen.getByRole("button", { name: "Public entry" });
    const target = screen.getByRole("button", { name: "Target" });

    target.focus();
    await user.keyboard("{ArrowUp}");
    expect(entry).toHaveFocus();
    await user.keyboard("{End}");
    expect(target).toHaveFocus();
    await user.keyboard("{Home}");
    expect(entry).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(target).toHaveFocus();
  });

  it("renders the mobile strip and selects its stage", async () => {
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(<FormStageNavigationMobile stages={stages} activeId="target" onSelect={onSelect} {...statusLabels} />);

    await user.click(screen.getByRole("button", { name: "Public entry" }));

    expect(onSelect).toHaveBeenCalledWith("entry");
    expect(screen.getByRole("button", { name: "Target" })).toHaveAttribute("aria-current", "step");
    expect(screen.getByRole("navigation")).toHaveClass("sticky", "overflow-x-auto");
  });
});
