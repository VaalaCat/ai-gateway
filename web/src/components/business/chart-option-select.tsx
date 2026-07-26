"use client";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

export interface ChartOption<V extends string> {
  value: V;
  label: string;
}

interface ChartOptionSelectProps<V extends string> {
  value: V;
  onValueChange: (v: V) => void;
  options: ReadonlyArray<ChartOption<V>>;
  /** trigger 内灰色前缀(如「指标」),多个 Select 并排时的语境标识;同时用作 aria-label */
  label: string;
  className?: string;
}

/** 图表卡头部统一的枚举切换器:窄屏不超宽,替代原先的 Tabs/ToggleGroup 平铺按钮 */
export function ChartOptionSelect<V extends string>({
  value,
  onValueChange,
  options,
  label,
  className,
}: ChartOptionSelectProps<V>) {
  return (
    <Select value={value} onValueChange={(v) => onValueChange(v as V)}>
      <SelectTrigger size="sm" aria-label={label} className={cn("w-fit gap-1.5", className)}>
        <span className="text-muted-foreground">{label} ·</span>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
