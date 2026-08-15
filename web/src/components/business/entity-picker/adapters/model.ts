import { useMemo } from "react";
import { useBYOKAvailableModels } from "@/lib/api/byok-channels";
import type { ModelConfig } from "@/lib/types";
import type { EntityAdapter, EntityListParams, EntityQueryState } from "../types";

// model 不需要单项 lookup:它的 value 就是 model_name,用 labelForValue 同步回显。
const EMPTY_ONE: EntityQueryState<ModelConfig> = {
  data: undefined,
  isLoading: false,
  isError: false,
  error: null,
  refetch: async () => undefined,
};

export const modelAdapter: EntityAdapter<ModelConfig> = {
  name: "model",
  // 数据源用 /private-channels/available-models(useBYOKAvailableModels):
  // 该端点虽挂在 BYOK 命名空间,但实现是 ModelConfig.ListAll()、session(JWT) 鉴权,
  // 返回完整 admin 模型目录名——普通用户也可访问,故三页模型筛选器共用它。
  // (原先用 useModels→/admin/models 是 admin-only,非管理员 403 导致下拉恒空。)
  // 该端点无服务端 search/分页,且 picker 是 Command shouldFilter={false},
  // 所以在这里做前端 search 过滤 + 截断到 page_size。
  useList: ({ search, page_size, enabled = true }: EntityListParams) => {
    const q = useBYOKAvailableModels({ enabled });
    const data = useMemo(() => {
      const all = q.data?.models ?? [];
      const kw = (search ?? "").trim().toLowerCase();
      const hit = kw ? all.filter((n) => n.toLowerCase().includes(kw)) : all;
      return { data: hit.slice(0, page_size).map((model_name) => ({ model_name })) };
    }, [q.data, search, page_size]);
    return { ...q, data } as unknown as ReturnType<EntityAdapter<ModelConfig>["useList"]>;
  },
  useOne: () => EMPTY_ONE,
  getValue: (item) => item.model_name,
  getLabel: (item) => item.model_name,
  labelForValue: (v) => v || undefined,
};
