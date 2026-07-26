import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ScopeBadge } from "./scope-badge";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/channels", () => ({ useChannels: () => ({ data: { data: [] } }) }));
vi.mock("@/components/business/entity-chip-list", () => ({
  EntityChipList: ({ entity, ids, scope }: { entity: string; ids: Array<number | string>; scope?: string }) => (
    <span>{`${entity}:${ids.join(",")}:${scope ?? "self"}`}</span>
  ),
}));

describe("ScopeBadge", () => {
  it("renders the global label when all five dimensions are empty", () => {
    render(<ScopeBadge scope={{
      channel_ids: [],
      private_channel_ids: [],
      model_names: [],
      group_ids: [],
      user_ids: [],
    }} />);

    expect(screen.getByText("scopeAll")).toBeInTheDocument();
  });

  it("renders every non-empty dimension with the correct entity and BYOK scope", () => {
    render(<ScopeBadge scope={{
      channel_ids: [1],
      private_channel_ids: [2],
      model_names: ["gpt-5"],
      group_ids: [3],
      user_ids: [4],
    }} />);

    expect(screen.getByText("channel:1:self")).toBeInTheDocument();
    expect(screen.getByText("byok-channel:2:all")).toBeInTheDocument();
    expect(screen.getByText("user-group:3:self")).toBeInTheDocument();
    expect(screen.getByText("user:4:self")).toBeInTheDocument();
    expect(screen.getByText("gpt-5")).toBeInTheDocument();
    expect(screen.queryByText("scopeAll")).not.toBeInTheDocument();
  });

  it("keeps legacy two-field scopes free of empty identity chips", () => {
    render(<ScopeBadge scope={{ channel_ids: [9], model_names: ["legacy"] }} />);

    expect(screen.getByText("channel:9:self")).toBeInTheDocument();
    expect(screen.getByText("legacy")).toBeInTheDocument();
    expect(screen.queryByText(/byok-channel:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/user-group:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/user:/)).not.toBeInTheDocument();
  });
});
