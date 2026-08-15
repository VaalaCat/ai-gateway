import { useState } from "react";
import type { ReactNode } from "react";
import { useDebounce } from "@/hooks/use-debounce";
import type { AdminScope, EntityAdapter } from "./types";

/** 选择类组件(单选/多选)共用:列表 + 防抖搜索 + 取 value/label/renderItem。 */
export function useEntityOptions(
  adapter: EntityAdapter<unknown>,
  opts: {
    scope: AdminScope;
    pageSize: number;
    ownerUserId?: number;
		apiServiceId?: number;
		apiRouteId?: number;
    enabled?: boolean;
  },
) {
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounce(search, 300);
  const list = adapter.useList({
    search: debouncedSearch,
    scope: opts.scope,
    ...(opts.ownerUserId !== undefined ? { ownerUserId: opts.ownerUserId } : {}),
		apiServiceId: opts.apiServiceId,
		apiRouteId: opts.apiRouteId,
    page_size: opts.pageSize,
    enabled: opts.enabled ?? true,
  });
  const items = list.data?.data ?? [];
  return {
    search,
    setSearch,
    items,
    isLoading: list.isLoading,
    isError: list.isError,
    error: list.error,
    refetch: list.refetch,
    getValue: (item: unknown): string => adapter.getValue(item),
    getLabel: (item: unknown): string => adapter.getLabel(item),
    renderItem: (item: unknown): ReactNode =>
      adapter.renderItem ? adapter.renderItem(item) : adapter.getLabel(item),
  };
}
