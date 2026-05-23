import { useUsers, useUser } from "@/lib/api/users";
import type { User } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const userAdapter: EntityAdapter<User> = {
  name: "user",
  useList: ({ search, page_size }: EntityListParams) =>
    useUsers({ search, page_size }) as ReturnType<EntityAdapter<User>["useList"]>,
  useOne: (id) =>
    useUser(id ? Number(id) : 0) as ReturnType<EntityAdapter<User>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.username,
};
