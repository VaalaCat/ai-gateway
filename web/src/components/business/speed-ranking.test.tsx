import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SpeedRanking } from "./speed-ranking";

vi.mock("@/components/business/model-name", () => ({
  ModelName: ({ name }: { name: string }) => <span data-testid="model-name">{name}</span>,
}));

const labels = {
  title: "TPS ranking",
  rankLabel: "Rank",
  nameLabel: "Name",
  valueLabel: "TPS p5",
  emptyText: "No samples",
  topN: 5,
};

describe("SpeedRanking TPS lower tail", () => {
  it("sorts positive p5 values descending and leaves zero samples unranked", () => {
    render(<SpeedRanking {...labels} entity="channel" metric="tps" rows={[
      { name: "slow", ttft_ms: 0, tps: 0, tps_p5: 5 },
      { name: "no-sample", ttft_ms: 0, tps: 0, tps_p5: 0 },
      { name: "fast", ttft_ms: 0, tps: 0, tps_p5: 50 },
    ]} />);
    const rows = screen.getAllByRole("row");
    expect(rows[1]).toHaveTextContent("fast");
    expect(rows[2]).toHaveTextContent("slow");
    expect(rows[3]).toHaveTextContent("no-sample");
    expect(rows[3]).toHaveTextContent("—");
  });

  it("renders missing and zero samples with em dashes instead of false ranks", () => {
    render(<SpeedRanking {...labels} entity="channel" metric="tps" rows={[
      { name: "missing", ttft_ms: 0, tps: 0 },
      { name: "zero", ttft_ms: 0, tps: 0, tps_p5: 0 },
    ]} />);
    expect(screen.queryByText("No samples")).not.toBeInTheDocument();
    expect(screen.getAllByText("—")).toHaveLength(4);
  });

  it("applies top n to the combined ranked and unranked rows", () => {
    render(<SpeedRanking {...labels} topN={2} entity="channel" metric="tps" rows={[
      { name: "first", ttft_ms: 0, tps: 0, tps_p5: 50 },
      { name: "second", ttft_ms: 0, tps: 0, tps_p5: 25 },
      { name: "missing", ttft_ms: 0, tps: 0 },
    ]} />);
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(screen.queryByText("missing")).not.toBeInTheDocument();
  });

  it("renders model rows with ModelName", () => {
    render(<SpeedRanking {...labels} entity="model" metric="tps" rows={[
      { name: "gpt-5", ttft_ms: 0, tps: 0, tps_p5: 50 },
    ]} />);

    expect(screen.getByTestId("model-name")).toHaveTextContent("gpt-5");
  });

  it("renders channel rows as text without ModelName", () => {
    render(<SpeedRanking {...labels} entity="channel" metric="tps" rows={[
      { name: "channel-us", ttft_ms: 0, tps: 0, tps_p5: 50 },
    ]} />);

    expect(screen.getByText("channel-us")).toBeInTheDocument();
    expect(screen.queryByTestId("model-name")).not.toBeInTheDocument();
  });
});
