import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { RefCombobox } from "./ref-combobox";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/model-routings", () => ({
  useRoutingCandidates: () => ({ data: { models: ["same"], global_routings: ["same", "self"] } }),
  useRoutingCandidatesByToken: () => ({ data: undefined }),
}));

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe("explicit routing member selection", () => {
  it("keeps same-name model and routing choices separate", async () => {
    const onChange = vi.fn();
    render(<RefCombobox value="" kind="model" onChange={onChange} />);
    await userEvent.click(screen.getByRole("combobox"));
    const options = screen.getAllByRole("option").filter((node) => node.textContent?.includes("same"));
    expect(options).toHaveLength(2);
    await userEvent.click(options[0]);
    expect(onChange).toHaveBeenLastCalledWith("same", "model");
    await userEvent.click(screen.getByRole("combobox"));
    await userEvent.click(screen.getAllByRole("option").find((node) => node.textContent?.includes("refKindRoutingShort") && node.textContent?.includes("same"))!);
    expect(onChange).toHaveBeenLastCalledWith("same", "routing");
  });

  it("excludes self as a routing while retaining the same-name real model", async () => {
    render(<RefCombobox value="" kind="model" onChange={vi.fn()} excludeSelf="same" alreadyAdded={[{ ref: "same", kind: "routing" }]} />);
    await userEvent.click(screen.getByRole("combobox"));
    const options = screen.getAllByRole("option").filter((node) => node.textContent?.includes("same"));
    expect(options).toHaveLength(1);
    expect(options[0]).not.toHaveAttribute("data-disabled", "true");
    expect(options[0]).toHaveTextContent("refUnderlyingModel");
  });
});
