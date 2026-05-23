"use client";

import { MetricTile, type MetricTileViz, type MetricTileThreshold } from "@/components/business/metric-tile";
import { normalize0to100 } from "@/lib/utils/normalize";
import { cn } from "@/lib/utils";

export interface KpiItem {
  key: string;
  label: string;
  value: string | number;
  sublabel?: string;
  onClick?: () => void;

  /** 原值数组,KpiGrid 内部 normalize0to100 → line viz */
  spark?: number[];
  /** 0-100 → ratio viz(扇形);搭配 threshold.semantic 用 */
  ratio?: number;
  /** 0-100 → progress viz(条形) */
  progress?: number;

  threshold?: {
    warn: number;
    critical: number;
    /** 默认 'high-bad';'low-bad' 时 KpiGrid 内部反向取 100-ratio 喂给底层 */
    semantic?: "high-bad" | "low-bad";
  };

  loading?: boolean;
}

interface KpiGridProps {
  items: KpiItem[];
  /** 默认 3 */
  gap?: 2 | 3 | 4;
  className?: string;
}

const LG_COLS = {
  3: "lg:grid-cols-3",
  4: "lg:grid-cols-4",
  5: "lg:grid-cols-5",
  6: "lg:grid-cols-6",
} as const;

const GAP_CLS = {
  2: "gap-2",
  3: "gap-3",
  4: "gap-4",
} as const;

function translateItem(item: KpiItem): {
  viz: MetricTileViz | undefined;
  threshold: MetricTileThreshold | undefined;
} {
  // spark → line viz(归一)
  if (item.spark !== undefined) {
    return {
      viz: { kind: "line", values: normalize0to100(item.spark) },
      threshold: item.threshold
        ? { warn: item.threshold.warn, critical: item.threshold.critical }
        : undefined,
    };
  }
  // ratio → ratio viz;low-bad 语义反向
  if (item.ratio !== undefined) {
    const semantic = item.threshold?.semantic ?? "high-bad";
    const vizPercent = semantic === "low-bad" ? 100 - item.ratio : item.ratio;
    // low-bad 语义:vizPercent 和 threshold 都反向,让 MetricTile 的"高阈警示"
    // 内部逻辑等价于"低于 warn/critical 阈值时警示".
    return {
      viz: { kind: "ratio", percent: vizPercent },
      threshold: item.threshold
        ? semantic === "low-bad"
          ? { warn: 100 - item.threshold.warn, critical: 100 - item.threshold.critical }
          : { warn: item.threshold.warn, critical: item.threshold.critical }
        : undefined,
    };
  }
  // progress → progress viz
  if (item.progress !== undefined) {
    return {
      viz: { kind: "progress", percent: item.progress },
      threshold: item.threshold
        ? { warn: item.threshold.warn, critical: item.threshold.critical }
        : undefined,
    };
  }
  return { viz: { kind: "none" }, threshold: undefined };
}

/**
 * Hero KPI 卡片网格,数据驱动.
 * - sm grid-cols-2, md grid-cols-3, lg 按 items.length(3-6 自动)
 * - 内部把 KpiItem 业务语义(spark/ratio/progress)翻译成 MetricTile.viz
 * - low-bad threshold semantic 在内部反向喂给底层 ratio viz
 */
export function KpiGrid({ items, gap = 3, className }: KpiGridProps) {
  const cap = Math.max(3, Math.min(6, items.length)) as 3 | 4 | 5 | 6;
  const lgCol = LG_COLS[cap];

  return (
    <div
      className={cn(
        "grid grid-cols-2 md:grid-cols-3",
        GAP_CLS[gap],
        lgCol,
        className,
      )}
    >
      {items.map((it) => {
        const { viz, threshold } = translateItem(it);
        return (
          <MetricTile
            key={it.key}
            label={it.label}
            value={it.value}
            sublabel={it.sublabel}
            viz={viz}
            threshold={threshold}
            onClick={it.onClick}
            loading={it.loading}
          />
        );
      })}
    </div>
  );
}
