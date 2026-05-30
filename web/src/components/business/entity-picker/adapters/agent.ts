import { useAgents } from "@/lib/api/agents";
import type { Agent } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const agentAdapter: EntityAdapter<Agent> = {
  name: "agent",
  useList: ({ search, page_size }: EntityListParams) =>
    useAgents({ search, page_size }) as ReturnType<EntityAdapter<Agent>["useList"]>,
  // value 是 agent_id(字符串),useAgent 只能按数字主键查,无法用 agent_id 解析。
  // 从列表派生单项(find by agent_id),复用列表缓存。
  useOne: (id) => {
    const q = useAgents({ page_size: 100 });
    const item = id
      ? q.data?.data?.find((a) => (a.agent_id ?? String(a.id)) === id)
      : undefined;
    return { ...q, data: item } as unknown as ReturnType<EntityAdapter<Agent>["useOne"]>;
  },
  getValue: (item) => item.agent_id ?? String(item.id),
  getLabel: (item) => item.name || item.agent_id || "",
  renderItem: (item) => `${item.name} (${item.agent_id})`,
  labelForValue: (v) => v,
};
