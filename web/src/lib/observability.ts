import type { EntityName } from "@/components/business/entity-picker/registry";

/**
 * 把后端 limiter bucket key 解析成可渲染的资源。
 * bucket 形态：
 *   "shared"          -> 全局共享桶
 *   "u:7"             -> per_user
 *   "g:9"             -> per_group
 *   "c:admin:42"      -> per_channel（中段是 channel 的 scope/owner，末段是 channel id）
 *   "cu:admin:42:7"   -> per_channel_user（末段是 user id，但归属仍以 channel 为锚点展示）
 *
 * entity.type 取 EntityAdapter registry 实际的 key（注意 user-group 是连字符）。
 */
export function parseBucket(b: string): {
  kind: "shared" | "user" | "group" | "channel" | "channel_user" | "unknown";
  entity?: { type: Extract<EntityName, "user" | "channel" | "user-group">; id: number };
} {
  if (b === "shared") return { kind: "shared" };
  const p = b.split(":");
  if (p[0] === "u") return { kind: "user", entity: { type: "user", id: Number(p[1]) } };
  if (p[0] === "g") return { kind: "group", entity: { type: "user-group", id: Number(p[1]) } };
  if (p[0] === "c") return { kind: "channel", entity: { type: "channel", id: Number(p[2]) } };
  if (p[0] === "cu")
    return { kind: "channel_user", entity: { type: "channel", id: Number(p[2]) } };
  return { kind: "unknown" };
}
