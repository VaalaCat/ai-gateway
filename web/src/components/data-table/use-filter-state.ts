"use client";

import { useCallback, useMemo } from "react";
import { useSearchParams } from "next/navigation";
import {
  useSearchParamPatch,
  type PatchSearchParams,
  type SearchParamPatch,
} from "./use-search-param-patch";
import type { FilterSpec, FilterValues } from "./filter-spec";
import { parseNonNegativeDecimal } from "@/lib/utils/decimal";

export interface UseFilterStateOptions {
  /** 初次默认值（仅在 URL 缺该键时生效，不写入 URL）。 */
  defaults?: FilterValues;
  /** 任意 value 变更时清除 URL 上的 ?page=（默认 true）。 */
  resetPageOnChange?: boolean;
  /** 任意 value 变更时清除的页码 URL key，默认 page。 */
  resetPageKey?: string;
  /** 与同页其他 URL 状态共享尚未 commit 的目标，避免连续 replace 丢更新。 */
  patchSearchParams?: PatchSearchParams;
}

/**
 * useFilterState 把 FilterSpec 与 URL searchParams 双向绑定。
 *
 * - 读：从 URL 解析（time kind 读 start/end 两键，其余直接同名读）。
 * - 写：via router.replace（不推历史栈）。空 / undefined / 0 不写入 URL。
 *       默认值不写入 URL（避免冗余）。
 * - filter 变更默认 reset ?page=。
 */
export function useFilterState<S extends FilterSpec>(
  spec: S,
  opts: UseFilterStateOptions = {},
): [FilterValues, (next: Partial<FilterValues>) => void] {
  const searchParams = useSearchParams();
  const defaultPatchSearchParams = useSearchParamPatch();
  const {
    defaults,
    resetPageOnChange = true,
    resetPageKey = "page",
    patchSearchParams = defaultPatchSearchParams,
  } = opts;

  const values = useMemo<FilterValues>(() => {
    const v: FilterValues = { ...(defaults ?? {}) };
    for (const [key, def] of Object.entries(spec)) {
      if (def.kind === "time") {
        const s = searchParams.get("start");
        const e = searchParams.get("end");
        if (s !== null) {
          const start = parseNonNegativeDecimal(s);
          if (start === undefined) delete v.start;
          else v.start = start;
        }
        if (e !== null) {
          const end = parseNonNegativeDecimal(e);
          if (end === undefined) delete v.end;
          else v.end = end;
        }
      } else {
        const raw = searchParams.get(key);
        if (raw !== null) v[key] = raw;
      }
    }
    return v;
  }, [spec, searchParams, defaults]);

  const setValues = useCallback(
    (next: Partial<FilterValues>) => {
      const patch: SearchParamPatch = { ...next };
      if (resetPageOnChange) patch[resetPageKey] = undefined;
      patchSearchParams(patch);
    },
    [patchSearchParams, resetPageKey, resetPageOnChange],
  );

  return [values, setValues];
}
