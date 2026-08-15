import { useAPIService, useAPIServices, type APIService } from "@/lib/api/api-services";
import type { EntityAdapter, EntityListParams } from "../types";
import { renderAPIEntityItem } from "./api-entity-presentation";

export const apiServiceAdapter: EntityAdapter<APIService> = {
  name: "api-service",
  useList: ({ search, page_size, enabled = true }: EntityListParams) =>
    useAPIServices({ search, page_size }, { enabled }),
  useOne: (id) =>
    useAPIService(Number(id)),
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  renderItem: (item) => renderAPIEntityItem(item.name, item.slug, item.status),
};
