import { useUserGroups } from "@/lib/api/user-groups";
import type { UserGroup } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const userGroupAdapter: EntityAdapter<UserGroup> = {
  name: "user-group",
  useList: ({ search, page_size }: EntityListParams) =>
    useUserGroups({ search, pageSize: page_size }) as ReturnType<EntityAdapter<UserGroup>["useList"]>,
  // user-groups module has no single-item lookup needed for picker
  useOne: () =>
    ({
      data: undefined,
      isLoading: false,
      isSuccess: false,
      isError: false,
    }) as ReturnType<EntityAdapter<UserGroup>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
};
