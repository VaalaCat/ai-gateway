import { useAPIRoute, useAPIRoutes, type APIRoute } from "@/lib/api/api-services";
import type { EntityAdapter, EntityListParams } from "../types";
import { renderAPIEntityItem } from "./api-entity-presentation";

const validID = (id: number | undefined): id is number =>
  typeof id === "number" && Number.isSafeInteger(id) && id > 0;

export const apiRouteAdapter: EntityAdapter<APIRoute> = {
  name: "api-route",
  useList: ({ apiServiceId, search, page_size, enabled = true }: EntityListParams) => {
    const serviceID = validID(apiServiceId) ? apiServiceId : 0;
    return useAPIRoutes(
      { api_service_id: serviceID, search, page_size },
      { enabled: enabled && validID(apiServiceId) },
    );
  },
  // 管理 API 的 Route detail lookup 自带 RBAC，回显已有 binding 不要求再次提供父 Service。
  useOne: (id) =>
    useAPIRoute(Number(id)),
  getValue: (item) => String(item.id),
  getLabel: (item) => item.slug,
  renderItem: (item) => renderAPIEntityItem(item.slug, item.upstream_path, item.status),
};
