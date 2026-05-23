"use client";

import { RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import { DateRangeInputs } from "@/components/business/date-range-inputs";
import type { ObsGranularity, ObsRange } from "@/lib/types/observability";
import { tsToDateStr, dateStrToTs } from "@/lib/utils/date-range";

export type { ObsGranularity, ObsRange };

const HOUR_MAX_WINDOW = 7 * 86_400;

function clampForHour(r: ObsRange): { range: ObsRange; clamped: boolean } {
  if (r.gran !== "hour") return { range: r, clamped: false };
  if (r.end - r.start <= HOUR_MAX_WINDOW) return { range: r, clamped: false };
  return { range: { ...r, start: r.end - HOUR_MAX_WINDOW }, clamped: true };
}

interface ObservabilityHeaderProps {
  title: string;
  subtitle?: string;
  range: ObsRange;
  onRangeChange: (r: ObsRange) => void;
  onRefresh: () => void;
  refreshing?: boolean;
  showGranularity?: boolean;
}

export function ObservabilityHeader({
  title,
  subtitle,
  range,
  onRangeChange,
  onRefresh,
  refreshing = false,
  showGranularity = true,
}: ObservabilityHeaderProps) {
  const tRange = useTranslations("monitoring.range");
  const startStr = tsToDateStr(range.start);
  const endStr = tsToDateStr(range.end);

  const emitClampToast = () => toast.info(tRange("hourWindowClamped"));

  const handleStartChange = (s: string) => {
    const next: ObsRange = { ...range, start: dateStrToTs(s, false) };
    const result = clampForHour(next);
    if (result.clamped) emitClampToast();
    onRangeChange(result.range);
  };
  const handleEndChange = (s: string) => {
    const next: ObsRange = { ...range, end: dateStrToTs(s, true) };
    const result = clampForHour(next);
    if (result.clamped) emitClampToast();
    onRangeChange(result.range);
  };
  const handleGranChange = (v: string) => {
    if (v !== "day" && v !== "hour") return;
    const next: ObsRange = { ...range, gran: v };
    const result = clampForHour(next);
    if (result.clamped) emitClampToast();
    onRangeChange(result.range);
  };

  return (
    <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
      <div className="space-y-1">
        <h1 className="text-2xl font-bold">{title}</h1>
        {subtitle && <p className="text-muted-foreground">{subtitle}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        {showGranularity && (
          <Tabs value={range.gran} onValueChange={handleGranChange}>
            <TabsList>
              <TabsTrigger value="day">{tRange("day")}</TabsTrigger>
              <TabsTrigger value="hour">{tRange("hour")}</TabsTrigger>
            </TabsList>
          </Tabs>
        )}
        <DateRangeInputs
          compact
          startDate={startStr}
          endDate={endStr}
          onStartDateChange={handleStartChange}
          onEndDateChange={handleEndChange}
        />
        <Button
          variant="outline"
          size="sm"
          onClick={onRefresh}
          disabled={refreshing}
          aria-label="Refresh"
        >
          <RefreshCw className={refreshing ? "animate-spin" : ""} />
        </Button>
      </div>
    </div>
  );
}
