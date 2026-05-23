"use client";

import { cn } from "@/lib/utils";
import { useBreakpoint, type Breakpoint } from "@/lib/hooks/use-breakpoint";
import { downsamplePoints } from "@/lib/utils/data-glyph-downsample";

const KIND_PREFIX = {
  line: "l",
  bar: "b",
  pie: "p",
  ring: "r",
} as const;

function clampPct(n: number): number {
  if (Number.isNaN(n)) return 0;
  return Math.min(Math.max(n, 0), 100);
}

/**
 * 把归一后的 [0..100] 浮点夹到 0-100 整数,作为 datatype font ligature 的输入.
 * datatype font 期望 `{l:N,N,...}` 里 N ∈ 0-100(每个点的百分比高度),
 * 不能再压到 0-9——那样字体把所有值都当 0-9% 渲染,几乎贴底.
 */
function toPct0to100(n: number): number {
  return Math.round(clampPct(n));
}

interface BaseProps {
  title?: string;
  className?: string;
  /** 不传 = 用全量;传则按 useBreakpoint() 选 target 点数走 LTTB 降采样 */
  targetByBreakpoint?: Partial<Record<Breakpoint, number>>;
}

export type DataGlyphProps =
  | (BaseProps & { kind: "line"; values: number[] })
  | (BaseProps & { kind: "bar"; values: number[] })
  | (BaseProps & { kind: "pie"; value: number })
  | (BaseProps & { kind: "ring"; value: number });

function isEmpty(p: DataGlyphProps): boolean {
  if (p.kind === "line" || p.kind === "bar") {
    return !p.values || p.values.length === 0;
  }
  return p.value === undefined || Number.isNaN(p.value);
}

export function DataGlyph(props: DataGlyphProps) {
  const bp = useBreakpoint();

  if (isEmpty(props)) {
    return (
      <span className={cn("text-muted-foreground", props.className)}>—</span>
    );
  }

  const prefix = KIND_PREFIX[props.kind];

  let body: string;
  let ariaPointCount: number | undefined;
  if (props.kind === "line" || props.kind === "bar") {
    const target = props.targetByBreakpoint?.[bp];
    const values =
      target !== undefined && props.values.length > target
        ? downsamplePoints(props.values, target)
        : props.values;
    body = values.map(toPct0to100).join(",");
    ariaPointCount = values.length;
  } else {
    body = String(toPct0to100(props.value));
  }

  const text = `{${prefix}:${body}}`;

  const aria =
    props.title ??
    (props.kind === "line" || props.kind === "bar"
      ? `${props.kind} chart, ${ariaPointCount} points`
      : `${props.kind} ${Math.round(clampPct(props.value))}%`);

  return (
    <span
      className={cn(
        "font-datatype tabular-nums leading-none",
        props.className,
      )}
      role="img"
      aria-label={aria}
    >
      {text}
    </span>
  );
}
