import { describe, expect, it } from "vitest";

import type { AgentRoute, OnlineAgentInfo } from "@/lib/types";

import { agentIdForTarget, defaultAgentRouteTarget } from "./agent-route-target";

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

function online(agentId: string, tags: string): OnlineAgentInfo {
  return { agent_id: agentId, name: agentId, tags, http_addresses: "", last_seen: 0 };
}

describe("defaultAgentRouteTarget", () => {
  it("returns the agent of a catch-all rule that targets one agent", () => {
    expect(defaultAgentRouteTarget([route({ agent_id: "agent-east" })])).toEqual({
      agentId: "agent-east",
      agentTag: "",
    });
  });

  it("returns the tag of a catch-all rule that targets a tag", () => {
    expect(defaultAgentRouteTarget([route({ agent_tag: "edge" })])).toEqual({
      agentId: "",
      agentTag: "edge",
    });
  });

  it("ignores model-specific rules", () => {
    const routes = [
      route({ id: 1, model: "gpt-4", agent_id: "agent-east" }),
      route({ id: 2, agent_tag: "edge" }),
    ];

    expect(defaultAgentRouteTarget(routes)).toEqual({ agentId: "", agentTag: "edge" });
  });

  it.each([undefined, [], [route({ model: "gpt-4", agent_id: "agent-east" })]])(
    "is empty without a catch-all rule (%j)",
    (routes) => {
      expect(defaultAgentRouteTarget(routes as AgentRoute[] | undefined)).toEqual({
        agentId: "",
        agentTag: "",
      });
    },
  );
});

describe("agentIdForTarget", () => {
  it("returns the agent of an agent target as-is", () => {
    expect(agentIdForTarget({ agentId: "agent-east", agentTag: "" }, undefined)).toBe("agent-east");
  });

  it("picks the online member with the smallest agent_id for a tag target", () => {
    const agents = [
      online("edge-2", "edge"),
      online("edge-1", "us-west, edge"),
      online("core-1", "edge-1"),
    ];

    expect(agentIdForTarget({ agentId: "", agentTag: "edge" }, agents)).toBe("edge-1");
  });

  it("stays local when the embedded agent carries the tag", () => {
    const agents = [online("embedded", "edge"), online("edge-1", "edge")];

    expect(agentIdForTarget({ agentId: "", agentTag: "edge" }, agents)).toBe("");
  });

  it.each([undefined, []])("stays local when no member is online (%j)", (agents) => {
    expect(agentIdForTarget({ agentId: "", agentTag: "edge" }, agents as OnlineAgentInfo[] | undefined)).toBe("");
  });

  it("is empty without a target", () => {
    expect(agentIdForTarget({ agentId: "", agentTag: "" }, [online("edge-1", "edge")])).toBe("");
  });
});
