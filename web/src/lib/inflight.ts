import type { GlobalInflightRow } from "@/lib/types";

export interface ObservedInflightRequest {
  row: GlobalInflightRow;
  observedAt: number;
  status: "current" | "stale" | "departed";
}

/** 使用节点与本地编号生成请求标识。 */
export function inflightKey(row: Pick<GlobalInflightRow, "agent_id" | "id">): string {
  // 1. 使用节点与本地请求编号定位请求。
  return `${row.agent_id}:${row.id}`;
}

/** 接受成功节点的新请求，保留未获取节点的数据和已选请求的最后详情。 */
export function reconcileInflightRequests(
  previous: ObservedInflightRequest[],
  rows: GlobalInflightRow[],
  successfulAgentIds: number[],
  selectedKey: string | null,
  observedAt: number,
): ObservedInflightRequest[] {
  // 1. 本轮有记录的节点同样属于已成功获取的节点。
  const successful = new Set([...successfulAgentIds, ...rows.map((row) => row.agent_id)]);
  const currentKeys = new Set(rows.map(inflightKey));
  const retained: ObservedInflightRequest[] = [];
  for (const request of previous) {
    const key = inflightKey(request.row);
    if (currentKeys.has(key)) continue;
    if (request.status === "departed" || successful.has(request.row.agent_id)) {
      if (key === selectedKey) retained.push({ ...request, status: "departed" });
    } else {
      retained.push({ ...request, status: "stale" });
    }
  }
  // 2. 节点和请求编号排序，不随快照遍历顺序跳动。
  return [
    ...rows.map((row): ObservedInflightRequest => ({ row, observedAt, status: "current" })),
    ...retained,
  ].sort((a, b) => a.row.agent_id - b.row.agent_id || a.row.id - b.row.id);
}

/** 读取当前尝试的渠道，尚未尝试时读取请求记录里的渠道。 */
export function inflightChannel(row: GlobalInflightRow) {
  // 1. 当前尝试携带名称和来源，不再另查可能同编号的公共渠道。
  const attempt = row.current_attempt;
  if (attempt && attempt.channel_id > 0) {
    return { id: attempt.channel_id, name: attempt.channel_name, private: ["private", "byok", "user"].includes(attempt.source) };
  }
  // 2. 私有渠道与公共渠道分别保留自己的编号。
  if ((row.view.private_channel_id ?? 0) > 0) {
    return { id: row.view.private_channel_id!, name: "", private: true };
  }
  return { id: row.view.channel_id > 0 ? row.view.channel_id : null, name: "", private: false };
}
