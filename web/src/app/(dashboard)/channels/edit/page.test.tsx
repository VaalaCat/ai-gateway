import { render } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import type { AgentRoute } from "@/lib/types";
import EditChannelPage from "./page";

const state = vi.hoisted(() => ({
  routes: undefined as AgentRoute[] | undefined,
  formProps: [] as Array<{ agentId?: string; agentTag?: string }>,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams("id=7"),
}));
vi.mock("@/lib/api/agent-routes", () => ({
  useAgentRoutes: () => ({ data: state.routes && { data: state.routes } }),
}));
vi.mock("@/components/channel/channel-form", () => ({
  ChannelForm: (props: { agentId?: string; agentTag?: string }) => {
    state.formProps.push(props);
    return null;
  },
}));

function route(overrides: Partial<AgentRoute> = {}): AgentRoute {
  return {
    id: 1,
    source_type: "channel",
    source_id: 7,
    model: "",
    agent_id: "",
    agent_tag: "",
    priority: 70,
    created_at: 0,
    updated_at: 0,
    ...overrides,
  };
}

beforeEach(() => {
  state.formProps.length = 0;
});

it("hands a tag-targeted catch-all rule to the form as agent_tag", () => {
  state.routes = [route({ agent_tag: "edge" })];

  render(<EditChannelPage />);

  expect(state.formProps.at(-1)).toMatchObject({ agentId: "", agentTag: "edge" });
});

it("hands an agent-targeted catch-all rule to the form as agent_id", () => {
  state.routes = [route({ agent_id: "agent-east" })];

  render(<EditChannelPage />);

  expect(state.formProps.at(-1)).toMatchObject({ agentId: "agent-east", agentTag: "" });
});

it("hands over no agent while the rules are still loading or define no catch-all", () => {
  for (const routes of [undefined, [], [route({ model: "gpt-4", agent_id: "agent-east" })]]) {
    state.routes = routes;

    render(<EditChannelPage />);

    expect(state.formProps.at(-1)).toMatchObject({ agentId: "", agentTag: "" });
  }
});
