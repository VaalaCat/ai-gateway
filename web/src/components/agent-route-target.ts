import type { AgentRoute, OnlineAgentInfo } from "@/lib/types";

/** 一条路由规则的去向:agent_id 与 agent_tag 二选一,另一个是空串。 */
export interface AgentRouteTarget {
  agentId: string;
  agentTag: string;
}

// master 进程内自带的 agent。它的请求在 master 本机执行,不经过 agent 通道。
const embeddedAgentId = "embedded";

/** 取某个来源的兜底规则(model 为空的那条)的去向;没有兜底规则时两项都为空。 */
export function defaultAgentRouteTarget(
  routes: AgentRoute[] | undefined,
): AgentRouteTarget {
  const route = routes?.find((r) => !r.model);
  if (!route) return { agentId: "", agentTag: "" };
  return { agentId: route.agent_id, agentTag: route.agent_tag };
}

/**
 * 路由去向对应的执行 agent,供只能选具体 agent 的选择器使用;空串表示在 master 本机执行。
 * agent_id 原样返回。agent_tag 对应一组 agent,取其中在线且 agent_id 最小的一个,
 * 与后端 fetch-models 的选法一致:内嵌 agent 命中标签或没有在线成员时走本机。
 */
export function agentIdForTarget(
  target: AgentRouteTarget,
  onlineAgents: OnlineAgentInfo[] | undefined,
): string {
  if (target.agentId) return target.agentId;
  if (!target.agentTag) return "";

  const members = (onlineAgents ?? []).filter((agent) =>
    hasTag(agent.tags, target.agentTag),
  );
  if (members.some((agent) => agent.agent_id === embeddedAgentId)) return "";
  return (
    members
      .map((agent) => agent.agent_id)
      .sort()
      .at(0) ?? ""
  );
}

/** tags 是逗号分隔的标签列表;只有整项相等才算命中。 */
function hasTag(tags: string, tag: string): boolean {
  return tags.split(",").some((member) => member.trim() === tag);
}
