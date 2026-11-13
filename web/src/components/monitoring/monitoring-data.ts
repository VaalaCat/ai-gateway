"use client";

import { useState } from "react";
import type { AgentBreakerCell, AgentQueueRow, BreakerBoardResponse, ChannelBreakerRow, DeliveryBoardResponse, LimiterBucketRow, LimiterUsageResponse } from "@/lib/types";

export type MonitoringAgentEntry =
  | { kind: "limiter"; bucket: LimiterBucketRow; cell: LimiterBucketRow["per_agent"][number] }
  | { kind: "breaker"; channel: ChannelBreakerRow; cell: AgentBreakerCell }
  | { kind: "delivery"; queue: AgentQueueRow };

export interface MonitoringAgentSample {
  agentID: number;
  agentName: string;
  updatedAt: number;
  entries: MonitoringAgentEntry[];
}

type MonitoringResponse = LimiterUsageResponse | BreakerBoardResponse | DeliveryBoardResponse;

// collectMonitoringAgentSamples 按节点整理本轮限流、熔断或投递明细。
export function collectMonitoringAgentSamples(data: MonitoringResponse, updatedAt: number): MonitoringAgentSample[] {
  // 1. 将各类节点明细放入对应节点。
  const samples = new Map<number, MonitoringAgentSample>();
  for (const agentID of data.successful_agent_ids) {
    samples.set(agentID, { agentID, agentName: "", updatedAt, entries: [] });
  }
  const append = (agentID: number, agentName: string, entry: MonitoringAgentEntry) => {
    let sample = samples.get(agentID);
    if (!sample) {
      sample = { agentID, agentName, updatedAt, entries: [] };
      samples.set(agentID, sample);
    }
    sample.agentName = agentName;
    sample.entries.push(entry);
  };
  if ("buckets" in data) {
    for (const bucket of data.buckets) {
      for (const cell of bucket.per_agent) append(cell.agent_id, cell.agent_name, { kind: "limiter", bucket, cell });
    }
  } else if ("channels" in data) {
    for (const channel of data.channels) {
      for (const cell of channel.agents) append(cell.agent_id, cell.agent_name, { kind: "breaker", channel, cell });
    }
  } else {
    for (const queue of data.agents) append(queue.agent_id, queue.agent_name, { kind: "delivery", queue });
  }
  return [...samples.values()];
}

// retainMonitoringAgents 更新成功节点，保留失败或缺席节点的最后明细。
export function retainMonitoringAgents(previous: MonitoringAgentSample[], data: MonitoringResponse, updatedAt: number): MonitoringAgentSample[] {
  // 1. 成功返回空列表的节点也清除上次明细。
  const successful = new Set(data.successful_agent_ids);
  const retained = previous.filter((sample) => !successful.has(sample.agentID));
  const current = collectMonitoringAgentSamples(data, updatedAt);
  for (const sample of current) {
    if (!sample.agentName) sample.agentName = previous.find((prior) => prior.agentID === sample.agentID)?.agentName ?? "";
  }
  const currentIDs = new Set(current.map((sample) => sample.agentID));
  return [...retained.filter((sample) => !currentIDs.has(sample.agentID)), ...current];
}

// useMonitoringAgentHistory 保存各节点最后成功明细，返回本轮未成功的节点。
export function useMonitoringAgentHistory(data: MonitoringResponse | undefined, updatedAt: number, requestFailed = false): MonitoringAgentSample[] {
  // 1. 每次成功获取后更新节点明细与其原始时间。
  const [history, setHistory] = useState<{ updatedAt: number; samples: MonitoringAgentSample[] }>({ updatedAt: 0, samples: [] });
  let samples = history.samples;
  if (data && updatedAt !== history.updatedAt) {
    samples = retainMonitoringAgents(history.samples, data, updatedAt);
    setHistory({ updatedAt, samples });
  }
  // 2. 整次请求失败时，缓存中的成功节点也列入过期区域。
  const successful = new Set(requestFailed ? [] : data?.successful_agent_ids ?? []);
  return samples.filter((sample) => !successful.has(sample.agentID));
}
