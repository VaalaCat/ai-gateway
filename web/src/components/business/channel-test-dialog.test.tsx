import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, expect, it, vi } from "vitest";

import type { AgentRoute, OnlineAgentInfo } from "@/lib/types";

import { ChannelTestDialog } from "./channel-test-dialog";

const state = vi.hoisted(() => ({
  routes: undefined as unknown,
  onlineAgents: undefined as unknown,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/agent-routes", () => ({ useAgentRoutes: () => ({ data: state.routes }) }));
vi.mock("@/lib/api/agents", () => ({ useOnlineAgents: () => ({ data: state.onlineAgents }) }));
vi.mock("./online-agent-select", () => ({ OnlineAgentSelect: () => null }));
vi.mock("./model-name", () => ({ ModelName: ({ name }: { name: string }) => <span>{name}</span> }));

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
});

beforeEach(() => {
  state.routes = undefined;
  state.onlineAgents = undefined;
});

function route(overrides: Partial<AgentRoute> = {}): AgentRoute {
  return {
    id: 1,
    source_type: "channel",
    source_id: 17,
    model: "",
    agent_id: "",
    agent_tag: "",
    priority: 70,
    created_at: 0,
    updated_at: 0,
    ...overrides,
  };
}

function online(agentId: string, tags: string): OnlineAgentInfo {
  return { agent_id: agentId, name: agentId, tags, http_addresses: "", last_seen: 0 };
}

async function runFirstModelTest(): Promise<unknown> {
  const testFn = vi.fn().mockResolvedValue({ success: true, time_cost: 1 });
  render(<ChannelTestDialog channelLike={{ id: 17, name: "Channel", type: 1, models: "custom-model", endpoints: "" }} testFn={testFn} supportsStream={false} agentSourceType="channel" open onOpenChange={vi.fn()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "test" }));
  await waitFor(() => expect(testFn).toHaveBeenCalledTimes(1));
  return testFn.mock.calls[0][0];
}

it.each([true, false])("sends Gemini endpoint_type from the shared admin/BYOK tester (stream support: %s)", async (supportsStream) => {
  const user = userEvent.setup();
  const testFn = vi.fn().mockResolvedValue({ success: true, time_cost: 1 });
  render(<ChannelTestDialog channelLike={{ id: 17, name: "Gemini", type: 24, models: "custom-model", endpoints: JSON.stringify({ generate_content: "/v1beta/models/{model}:{action}" }) }} testFn={testFn} supportsStream={supportsStream} agentSourceType={null} open onOpenChange={vi.fn()} />);
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: "apiTypeGemini" }));
  if (supportsStream) await user.click(screen.getByRole("switch"));
  await user.click(screen.getByRole("button", { name: "test" }));
  await waitFor(() => expect(testFn).toHaveBeenCalledWith({
    id: 17, model: "custom-model", endpoint_type: "gemini", ...(supportsStream ? { stream: true } : {}),
  }));
});

it.each([
  {
    rule: "an agent-targeted catch-all rule",
    routes: [route({ agent_id: "agent-east" })],
    onlineAgents: [online("edge-1", "edge")],
    wantAgentId: "agent-east",
  },
  {
    rule: "a tag-targeted catch-all rule",
    routes: [route({ agent_tag: "edge" })],
    onlineAgents: [online("edge-2", "edge"), online("edge-1", "us-west, edge"), online("core-1", "edge-1")],
    wantAgentId: "edge-1",
  },
])("tests on the agent that serves $rule", async ({ routes, onlineAgents, wantAgentId }) => {
  state.routes = { data: routes };
  state.onlineAgents = onlineAgents;

  expect(await runFirstModelTest()).toEqual({
    id: 17, model: "custom-model", endpoint_type: "chat-completion", agent_id: wantAgentId,
  });
});

it.each([
  {
    situation: "the embedded agent carries the rule's tag",
    onlineAgents: [online("embedded", "edge"), online("edge-1", "edge")],
  },
  {
    situation: "no member of the rule's tag is online",
    onlineAgents: [],
  },
])("tests locally when $situation", async ({ onlineAgents }) => {
  state.routes = { data: [route({ agent_tag: "edge" })] };
  state.onlineAgents = onlineAgents;

  expect(await runFirstModelTest()).toEqual({ id: 17, model: "custom-model", endpoint_type: "chat-completion" });
});
