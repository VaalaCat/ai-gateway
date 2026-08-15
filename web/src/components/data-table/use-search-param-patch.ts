"use client";

import { useCallback, useEffect, useRef } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

export type SearchParamPatchValue = string | number | boolean | null | undefined;
export type SearchParamPatch = Record<string, SearchParamPatchValue>;
export type PatchSearchParams = (patch: SearchParamPatch) => string;

interface PendingSearchTarget {
  pathname: string;
  target: string;
  knownSearches: Set<string>;
}

/**
 * Merges URL patches against the latest committed or still-pending target.
 *
 * Next navigation commits asynchronously. Keeping the issued targets local to
 * this hook prevents consecutive filter, Token, and pagination writes from
 * cloning the same stale searchParams snapshot and overwriting one another.
 */
export function useSearchParamPatch(): PatchSearchParams {
  const pathname = usePathname();
  const router = useRouter();
  const searchParams = useSearchParams();
  const committedSearch = searchParams.toString();
  const pendingRef = useRef<PendingSearchTarget | undefined>(undefined);

  useEffect(() => {
    const pending = pendingRef.current;
    if (!pending) return;
    if (pending.pathname !== pathname || !pending.knownSearches.has(committedSearch)) {
      pendingRef.current = undefined;
      return;
    }
    if (committedSearch === pending.target) {
      pendingRef.current = undefined;
    }
  }, [committedSearch, pathname]);

  return useCallback((patch: SearchParamPatch) => {
    const pending = pendingRef.current;
    const pendingApplies = pending?.pathname === pathname &&
      pending.knownSearches.has(committedSearch);
    const baseSearch = pendingApplies ? pending.target : committedSearch;
    const params = new URLSearchParams(baseSearch);

    for (const [key, value] of Object.entries(patch)) {
      if (value === undefined || value === null || value === "" || value === 0) {
        params.delete(key);
      } else {
        params.set(key, String(value));
      }
    }

    const target = params.toString();
    const knownSearches = pendingApplies
      ? new Set(pending.knownSearches)
      : new Set([committedSearch]);
    knownSearches.add(baseSearch);
    knownSearches.add(target);
    pendingRef.current = { pathname, target, knownSearches };

    router.replace(target ? `${pathname}?${target}` : pathname);
    return target;
  }, [committedSearch, pathname, router]);
}
