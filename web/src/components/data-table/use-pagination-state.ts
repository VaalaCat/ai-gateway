"use client";

import { useCallback } from "react";
import { useSearchParams } from "next/navigation";
import {
  useSearchParamPatch,
  type PatchSearchParams,
} from "./use-search-param-patch";
import { parsePositiveDecimal } from "@/lib/utils/decimal";

export interface UsePaginationStateOptions {
  patchSearchParams?: PatchSearchParams;
  /** 当前分页页码使用的 URL key，默认 page。 */
  pageKey?: string;
  /** 当前分页大小使用的 URL key，默认 page_size。 */
  pageSizeKey?: string;
}

/**
 * 只接受规范正十进制整数,否则回退(拒绝前导零、非十进制、unsafe、负数/小数/NaN/0)。
 */
function readPositiveInt(raw: string | null, fallback: number): number {
  return parsePositiveDecimal(raw) ?? fallback;
}

/**
 * 表格分页状态进 URL(?page=&page_size=,page 制,与后端 ListOptions 口径一致)。
 * 默认值不写 URL:page<=1 与 pageSize===defaultPageSize 时删参。
 * 与 useFilterState 协同:filter 变更时其 resetPageOnChange 会删 "page" → 自动回第 1 页。
 */
export function usePaginationState(
  defaultPageSize: number,
  options: UsePaginationStateOptions = {},
): [number, number, (page: number, pageSize: number) => void] {
  const searchParams = useSearchParams();
  const defaultPatchSearchParams = useSearchParamPatch();
  const patchSearchParams = options.patchSearchParams ?? defaultPatchSearchParams;
  const pageKey = options.pageKey ?? "page";
  const pageSizeKey = options.pageSizeKey ?? "page_size";

  const page = readPositiveInt(searchParams.get(pageKey), 1);
  const pageSize = readPositiveInt(searchParams.get(pageSizeKey), defaultPageSize);

  const setPagination = useCallback(
    (nextPage: number, nextSize: number) => {
      patchSearchParams({
        [pageKey]: nextPage <= 1 ? undefined : nextPage,
        [pageSizeKey]: nextSize === defaultPageSize ? undefined : nextSize,
      });
    },
    [defaultPageSize, pageKey, pageSizeKey, patchSearchParams],
  );

  return [page, pageSize, setPagination];
}
