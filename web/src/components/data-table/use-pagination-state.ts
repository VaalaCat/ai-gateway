"use client";

import { useCallback } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

/**
 * 只接受正整数,否则回退(拒绝负数/小数/NaN/0,防止 garbage pageCount 或后端 bind 400)。
 */
function readPositiveInt(raw: string | null, fallback: number): number {
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : fallback;
}

/**
 * 表格分页状态进 URL(?page=&page_size=,page 制,与后端 ListOptions 口径一致)。
 * 默认值不写 URL:page<=1 与 pageSize===defaultPageSize 时删参。
 * 与 useFilterState 协同:filter 变更时其 resetPageOnChange 会删 "page" → 自动回第 1 页。
 */
export function usePaginationState(
  defaultPageSize: number,
): [number, number, (page: number, pageSize: number) => void] {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const page = readPositiveInt(searchParams.get("page"), 1);
  const pageSize = readPositiveInt(searchParams.get("page_size"), defaultPageSize);

  const setPagination = useCallback(
    (nextPage: number, nextSize: number) => {
      const params = new URLSearchParams(searchParams.toString());
      if (nextPage <= 1) params.delete("page");
      else params.set("page", String(nextPage));
      if (nextSize === defaultPageSize) params.delete("page_size");
      else params.set("page_size", String(nextSize));
      const qs = params.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname);
    },
    [searchParams, router, pathname, defaultPageSize],
  );

  return [page, pageSize, setPagination];
}
