import { useEnabledTokenTemplates } from "@/lib/api/token-templates";
import type { TokenTemplate } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const tokenTemplateAdapter: EntityAdapter<TokenTemplate> = {
  name: "token-template",
  // 用用户可访问的 enabled 接口(/token-templates):管理员和普通用户都能用,且只返回启用的模板。
  // 该接口无 search 参数;模板量小(≤100),按 name 客户端过滤。
  useList: ({ search }: EntityListParams) => {
    const q = useEnabledTokenTemplates();
    const kw = (search ?? "").toLowerCase();
    const all = q.data?.data ?? [];
    const data = kw ? all.filter((t) => t.name.toLowerCase().includes(kw)) : all;
    return {
      ...q,
      data: q.data ? { ...q.data, data } : undefined,
    } as unknown as ReturnType<EntityAdapter<TokenTemplate>["useList"]>;
  },
  // 从同一份 enabled 列表派生单项(find by id),与 useList 共享 ["token-templates-enabled"] 缓存。
  useOne: (id) => {
    const q = useEnabledTokenTemplates();
    const item = id ? q.data?.data?.find((t) => String(t.id) === id) : undefined;
    return { ...q, data: item } as unknown as ReturnType<EntityAdapter<TokenTemplate>["useOne"]>;
  },
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
};
