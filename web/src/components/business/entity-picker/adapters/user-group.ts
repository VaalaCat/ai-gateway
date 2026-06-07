import { useUserGroups, useUserGroup } from "@/lib/api/user-groups";
import type { UserGroup } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const userGroupAdapter: EntityAdapter<UserGroup> = {
  name: "user-group",
  useList: ({ search, page_size }: EntityListParams) =>
    useUserGroups({ search, pageSize: page_size }) as ReturnType<EntityAdapter<UserGroup>["useList"]>,
  // 单项回显走 GET /admin/user-groups/:id（hook enabled: !!id），picker 触发按钮与
  // EntityLabel 才能把已选/已绑的 group id 解析成组名，否则只剩占位符 / #id。
  useOne: (id) =>
    useUserGroup(id ? Number(id) : 0) as ReturnType<EntityAdapter<UserGroup>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
};
