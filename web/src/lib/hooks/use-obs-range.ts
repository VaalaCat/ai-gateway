"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useMemo, useState } from "react";

import type { ObsRange, ObsGranularity } from "@/lib/types/observability";

const ONE_DAY = 86_400;

interface UseObsRange {
  range: ObsRange;
  setRange: (r: ObsRange) => void;
  refreshKey: number;
  refresh: () => void;
}

function resolveRange(
  sp: URLSearchParams | ReturnType<typeof useSearchParams>,
  defaults?: Partial<ObsRange>,
): ObsRange {
  const endParam = Number(sp.get("end"));
  const end = endParam || Math.floor(Date.now() / 1000);
  const startParam = Number(sp.get("start"));
  const start = startParam || end - ONE_DAY;
  const gran = (sp.get("gran") as ObsGranularity) || defaults?.gran || "day";
  return { start, end, gran };
}

export function useObsRange(defaults?: Partial<ObsRange>): UseObsRange {
  const router = useRouter();
  const pathname = usePathname();
  const sp = useSearchParams();

  // 派生 — URL 变(浏览器后退/外链跳转)立即同步,不再用 useState 镜像
  const range = useMemo(
    () => resolveRange(sp, defaults),
    [sp, defaults?.start, defaults?.end, defaults?.gran],
  );

  const setRange = useCallback(
    (r: ObsRange) => {
      const next = new URLSearchParams(sp.toString());
      next.set("start", String(r.start));
      next.set("end", String(r.end));
      next.set("gran", r.gran);
      router.replace(`${pathname}?${next.toString()}`);
    },
    [router, pathname, sp],
  );

  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);

  return { range, setRange, refreshKey, refresh };
}
