import { useTokens, useToken } from "@/lib/api/tokens";
import { useAuth } from "@/lib/auth";
import type { Token } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const tokenAdapter: EntityAdapter<Token> = {
  name: "token",
  useList: ({ search, scope, page_size }: EntityListParams) => {
    const { user } = useAuth();
    // scope === "self" → 带上当前用户 user_id 过滤；"all" → 不带，后端返回全部（仅 admin）。
    const selfUserID = scope === "self" ? user?.user_id : undefined;
    return useTokens({
      search,
      page_size,
      ...(selfUserID ? { user_id: selfUserID } : {}),
    }) as ReturnType<EntityAdapter<Token>["useList"]>;
  },
  useOne: (id) =>
    useToken(id ? Number(id) : 0) as ReturnType<EntityAdapter<Token>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  supportsAdminScope: true,
};
