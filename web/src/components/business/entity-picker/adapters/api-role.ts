import { useAPIRole, useAPIRoles, type APIRole } from "@/lib/api/api-access";
import type { EntityAdapter, EntityListParams } from "../types";
import { renderAPIEntityItem } from "./api-entity-presentation";

export const apiRoleAdapter: EntityAdapter<APIRole> = {
  name: "api-role",
  useList: ({ search, page_size, enabled = true }: EntityListParams) =>
    useAPIRoles(
      { search, page_size, assignable: true },
      { enabled },
    ),
  useOne: (id) =>
    useAPIRole(Number(id)),
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  renderItem: (item) => renderAPIEntityItem(item.name, item.key, item.status),
};
