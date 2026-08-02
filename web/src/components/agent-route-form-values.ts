export type AgentRouteSourceType = "token" | "channel";
export type AgentRouteTargetType = "agent_id" | "agent_tag";

export interface AgentRouteFormValues {
  sourceType: AgentRouteSourceType;
  sourceId: string;
  model: string;
  targetType: AgentRouteTargetType;
  targetValue: string;
}

export interface AgentRoutePayload {
  source_type: AgentRouteSourceType;
  source_id: number;
  model: string;
  agent_id: string;
  agent_tag: string;
}

export type AgentRoutePayloadResult =
  | { ok: true; payload: AgentRoutePayload }
  | { ok: false; field: "source_id" | "target_value" };

interface EditableAgentRoute {
  source_type: string;
  source_id: number;
  model: string;
  agent_id: string;
  agent_tag: string;
}

export function createAgentRouteFormValues(
  route: EditableAgentRoute | null,
): AgentRouteFormValues {
  const sourceType: AgentRouteSourceType = route?.source_type === "channel"
    ? "channel"
    : "token";
  const targetType: AgentRouteTargetType = route?.agent_tag
    ? "agent_tag"
    : "agent_id";

  return {
    sourceType,
    sourceId: route ? String(route.source_id) : "",
    model: route?.model ?? "",
    targetType,
    targetValue: route
      ? (targetType === "agent_tag" ? route.agent_tag : route.agent_id)
      : "",
  };
}

export function buildAgentRoutePayload(
  values: AgentRouteFormValues,
): AgentRoutePayloadResult {
  const sourceId = Number(values.sourceId.trim());
  if (!Number.isSafeInteger(sourceId) || sourceId <= 0) {
    return { ok: false, field: "source_id" };
  }

  const targetValue = values.targetValue.trim();
  if (!targetValue) {
    return { ok: false, field: "target_value" };
  }

  return {
    ok: true,
    payload: {
      source_type: values.sourceType,
      source_id: sourceId,
      model: values.model.trim(),
      agent_id: values.targetType === "agent_id" ? targetValue : "",
      agent_tag: values.targetType === "agent_tag" ? targetValue : "",
    },
  };
}
