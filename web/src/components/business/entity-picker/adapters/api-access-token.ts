import { useTokens } from "@/lib/api/tokens";
import { useAuth } from "@/lib/auth";
import type { Token } from "@/lib/types";

import type { EntityAdapter, EntityListParams } from "../types";

export const apiAccessTokenAdapter: EntityAdapter<Token> = {
  name: "api-access-token",
  useList: ({ search, scope, page_size, ownerUserId, enabled = true }: EntityListParams) => {
    const { user } = useAuth();
    const userID = ownerUserId ?? (scope === "self" ? user?.user_id : undefined);
    return useTokens({ search, page_size, api_role_mode: "explicit", ...(userID ? { user_id: userID } : {}) }, { enabled });
  },
  useOne: (id) => {
    const query = useTokens({ token_id: Number(id), page: 1, page_size: 1, api_role_mode: "explicit" }, { enabled: Boolean(id) });
    return { ...query, data: query.data?.data[0] };
  },
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  supportsAdminScope: true,
};
