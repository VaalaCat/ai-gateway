import { useTokens, useToken } from "@/lib/api/tokens";
import type { Token } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const tokenAdapter: EntityAdapter<Token> = {
  name: "token",
  useList: ({ search, page_size }: EntityListParams) =>
    useTokens({ search, page_size }) as ReturnType<EntityAdapter<Token>["useList"]>,
  useOne: (id) =>
    useToken(id ? Number(id) : 0) as ReturnType<EntityAdapter<Token>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  supportsAdminScope: true,
};
