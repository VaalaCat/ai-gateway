"use client";

import { useCallback, useState } from "react";

import type { ChartConfig } from "@/components/ui/chart";
import { ScrollableChartLegend } from "@/components/business/scrollable-chart-legend";

const EMPTY_HIDDEN_SERIES = new Set<string>();

/** 系列顺序内容变化时忽略旧隐藏集；状态携带 key，避免用 effect 同步派生状态。 */
export function useHiddenSeries(seriesOrder: readonly string[]) {
  const orderKey = JSON.stringify(seriesOrder);
  const [state, setState] = useState<{ orderKey: string; hidden: Set<string> }>(() => ({
    orderKey,
    hidden: new Set(),
  }));
  const hidden = state.orderKey === orderKey ? state.hidden : EMPTY_HIDDEN_SERIES;
  const toggle = useCallback((key: string) => {
    setState((prev) => {
      const next = new Set(prev.orderKey === orderKey ? prev.hidden : []);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return { orderKey, hidden: next };
    });
  }, [orderKey]);
  return { hidden, toggle };
}

interface ToggleableChartLegendProps {
  /** recharts ChartLegend content 回传的 payload */
  payload?: ReadonlyArray<{ value?: unknown; color?: string }>;
  config: ChartConfig;
  hidden: Set<string>;
  onToggle: (key: string) => void;
}

/** Recharts payload 到共享可滚动图例的兼容适配器。 */
export function ToggleableChartLegend({
  payload,
  config,
  hidden,
  onToggle,
}: ToggleableChartLegendProps) {
  if (!payload?.length) return null;
  return (
    <ScrollableChartLegend
      items={payload.map((entry) => {
        const key = String(entry.value);
        return {
          key,
          label: config[key]?.label ?? key,
          color: entry.color ?? config[key]?.color ?? "var(--muted-foreground)",
          hidden: hidden.has(key),
        };
      })}
      onToggle={onToggle}
    />
  );
}
