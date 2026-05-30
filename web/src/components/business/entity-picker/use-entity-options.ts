import { useState } from "react";
import type { ReactNode } from "react";
import { useDebounce } from "@/hooks/use-debounce";
import type { AdminScope, EntityAdapter } from "./types";

/** 选择类组件(单选/多选)共用:列表 + 防抖搜索 + 取 value/label/renderItem。 */
export function useEntityOptions(
  adapter: EntityAdapter<unknown>,
  opts: { scope: AdminScope; pageSize: number },
) {
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounce(search, 300);
  const list = adapter.useList({
    search: debouncedSearch,
    scope: opts.scope,
    page_size: opts.pageSize,
  });
  const items = list.data?.data ?? [];
  return {
    search,
    setSearch,
    items,
    isLoading: list.isLoading,
    getValue: (item: unknown): string => adapter.getValue(item),
    getLabel: (item: unknown): string => adapter.getLabel(item),
    renderItem: (item: unknown): ReactNode =>
      adapter.renderItem ? adapter.renderItem(item) : adapter.getLabel(item),
  };
}
