import type { ComponentType } from "react";

import type { EntityMeta } from "@/lib/api/insights";

import { AgentHeader } from "./headers/agent";
import { PlannedHeader } from "./headers/planned";

export type EntityType = "agent" | "channel" | "model" | "user" | "token";

export type BreakdownAxis = "model" | "channel" | "agent" | "user" | "token";

export interface EntityInsightConfig {
  renderHeader: ComponentType<{ meta: EntityMeta }>;
  showStageLatency: boolean;
  breakdownAxes: BreakdownAxis[];
  stage: "ready" | "planned";
}

export const INSIGHT_REGISTRY: Record<EntityType, EntityInsightConfig> = {
  agent: {
    renderHeader: AgentHeader,
    showStageLatency: true,
    breakdownAxes: ["model", "channel"],
    stage: "ready",
  },
  channel: {
    renderHeader: PlannedHeader,
    showStageLatency: true,
    breakdownAxes: ["model", "agent"],
    stage: "planned",
  },
  model: {
    renderHeader: PlannedHeader,
    showStageLatency: false,
    breakdownAxes: ["channel", "user"],
    stage: "planned",
  },
  user: {
    renderHeader: PlannedHeader,
    showStageLatency: false,
    breakdownAxes: ["model", "token"],
    stage: "planned",
  },
  token: {
    renderHeader: PlannedHeader,
    showStageLatency: false,
    breakdownAxes: ["model", "channel"],
    stage: "planned",
  },
};
