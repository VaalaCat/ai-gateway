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
  nowSec: number,
  granDefault?: ObsGranularity,
): ObsRange {
  const endParam = Number(sp.get("end"));
  const end = endParam || nowSec;
  const startParam = Number(sp.get("start"));
  const start = startParam || end - ONE_DAY;
  const gran = (sp.get("gran") as ObsGranularity) || granDefault || "day";
  return { start, end, gran };
}

export function useObsRange(defaults?: Partial<ObsRange>): UseObsRange {
  const router = useRouter();
  const pathname = usePathname();
  const sp = useSearchParams();

  // "now" 在挂载时捕获一次: 否则每次渲染 Date.now() 都变,range.end 漂移 → 下游
  // queryKey 每次渲染都变 → 全页数据无谓重取/闪烁(KPI 卡片瞬间 data=undefined 消失)。
  const [nowSec] = useState(() => Math.floor(Date.now() / 1000));
  const granDefault = defaults?.gran;

  // 派生 — URL 变(浏览器后退/外链跳转)立即同步,不再用 useState 镜像。
  // 依赖只取稳定值(sp / 捕获的 nowSec / 原始 gran),不依赖 defaults 对象身份,
  // 否则内联的 {gran:"day"} 每次渲染都是新引用会让 memo 失效。
  const range = useMemo(
    () => resolveRange(sp, nowSec, granDefault),
    [sp, nowSec, granDefault],
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
