import { useAgents, useAgent } from "@/lib/api/agents";
import type { Agent } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const agentAdapter: EntityAdapter<Agent> = {
  name: "agent",
  useList: ({ search, page_size }: EntityListParams) =>
    useAgents({ search, page_size }) as ReturnType<EntityAdapter<Agent>["useList"]>,
  useOne: (id) =>
    useAgent(id ? Number(id) : 0) as ReturnType<EntityAdapter<Agent>["useOne"]>,
  getValue: (item) => item.agent_id ?? String(item.id),
  getLabel: (item) => item.agent_id ?? item.name ?? "",
};
