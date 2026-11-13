import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { EntityLabel } from "./entity-label";

vi.mock("@/components/business/entity-picker/registry", () => ({
  ENTITY_ADAPTERS: {
    channel: { useOne: () => ({ data: { name: "Current entity name" } }), getLabel: (item: { name: string }) => item.name },
    agent: { useOne: () => ({ data: undefined }), getLabel: () => "", labelForValue: (id: string) => id },
  },
}));
vi.mock("@/components/business/entity-hover/entity-hover-card", () => ({ hasEntityHoverBody: () => false }));

describe("EntityLabel snapshot names", () => {
  it("preserves the resolved label and ID when no snapshot options are supplied", () => {
    render(<EntityLabel entity="channel" id={7} />);
    expect(screen.getByText("Current entity name")).toHaveTextContent("#7");
  });

  it("uses the recorded name while keeping the ID outside the truncated name", () => {
    render(<EntityLabel entity="channel" id={7} snapshotName="Name at request time" nameClassName="max-w-24 truncate" />);
    const name = screen.getByText("Name at request time");
    const id = screen.getByText("#7");
    expect(name).not.toContainElement(id);
    expect(name.parentElement).toContainElement(id);
    expect(screen.queryByText("Current entity name")).not.toBeInTheDocument();
  });

  it("uses a recorded node name without treating a numeric database ID as its business ID", () => {
    render(<EntityLabel entity="agent" id={undefined} snapshotName="Offline node" showId={false} />);
    expect(screen.getByText("Offline node")).toBeInTheDocument();
    expect(screen.queryByText(/^#/)).not.toBeInTheDocument();
  });
});
