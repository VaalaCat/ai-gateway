"use client";

import { useCallback, useMemo } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import type { FilterSpec, FilterValues } from "./filter-spec";

interface UseFilterStateOptions {
  /** 初次默认值（仅在 URL 缺该键时生效，不写入 URL）。 */
  defaults?: FilterValues;
  /** 任意 value 变更时清除 URL 上的 ?page=（默认 true）。 */
  resetPageOnChange?: boolean;
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
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const { defaults, resetPageOnChange = true } = opts;

  const values = useMemo<FilterValues>(() => {
    const v: FilterValues = { ...(defaults ?? {}) };
    for (const [key, def] of Object.entries(spec)) {
      if (def.kind === "time") {
        const s = searchParams.get("start");
        const e = searchParams.get("end");
        if (s) v.start = Number(s);
        if (e) v.end = Number(e);
      } else {
        const raw = searchParams.get(key);
        if (raw !== null) v[key] = raw;
      }
    }
    return v;
  }, [spec, searchParams, defaults]);

  const setValues = useCallback(
    (next: Partial<FilterValues>) => {
      const merged: FilterValues = { ...values, ...next };
      const params = new URLSearchParams(searchParams.toString());
      for (const [k, v] of Object.entries(merged)) {
        if (v === undefined || v === "" || v === 0) {
          params.delete(k);
        } else {
          params.set(k, String(v));
        }
      }
      if (resetPageOnChange) {
        params.delete("page");
      }
      router.replace(`${pathname}?${params.toString()}`);
    },
    [values, searchParams, router, pathname, resetPageOnChange],
  );

  return [values, setValues];
}
