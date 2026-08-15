"use client";

import { type ReactNode } from "react";
import { RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/layout/page-header";
import { Separator } from "@/components/ui/separator";
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  DateRangePicker,
  type DateRangeValue,
} from "@/components/business/date-picker/date-range-picker";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { ObsGranularity, ObsRange } from "@/lib/types/observability";
import { tsToDateStr, dateStrToTs } from "@/lib/utils/date-range";
import { switchLongRangeToDay } from "@/lib/utils/observability-range";

export type { ObsGranularity, ObsRange };

interface ObservabilityHeaderProps {
  title: string;
  subtitle?: string;
  range: ObsRange;
  onRangeChange: (r: ObsRange) => void;
  onRefresh: () => void;
  refreshing?: boolean;
  showGranularity?: boolean;
  maxDays?: number;
  scopeLabel?: string;
  scopeControls?: ReactNode;
  headerActions?: ReactNode;
  showPageHeader?: boolean;
}

export function ObservabilityHeader({
  title,
  subtitle,
  range,
  onRangeChange,
  onRefresh,
  refreshing = false,
  showGranularity = true,
  maxDays,
  scopeLabel,
  scopeControls,
  headerActions,
  showPageHeader = true,
}: ObservabilityHeaderProps) {
  const tRange = useTranslations("monitoring.range");
  const tb = useTranslations("billing");
  const startStr = tsToDateStr(range.start);
  const endStr = tsToDateStr(range.end);

  const emitAutoDayToast = () => toast.info(tRange("longRangeUsesDay"));

  const handleDateRangeChange = ({ startDate, endDate }: DateRangeValue) => {
    const next: ObsRange = {
      ...range,
      start: dateStrToTs(startDate, false),
      end: dateStrToTs(endDate, true),
    };
    const result = switchLongRangeToDay(next);
    if (result.adjusted) emitAutoDayToast();
    onRangeChange(result.range);
  };
  const handleGranChange = (v: string) => {
    if (v !== "day" && v !== "hour") return;
    const next: ObsRange = { ...range, gran: v };
    const result = switchLongRangeToDay(next);
    if (result.adjusted) emitAutoDayToast();
    onRangeChange(result.range);
  };

  return (
    <div className="flex flex-col gap-3">
      {showPageHeader ? <PageHeader title={title} description={subtitle} actions={headerActions} /> : null}
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-end">
        <div className="grid w-full grid-cols-[1fr_auto] items-center gap-2 sm:flex sm:w-auto sm:flex-wrap sm:justify-end">
          {showGranularity ? (
            <Tabs value={range.gran} onValueChange={handleGranChange}>
              <TabsList className="!h-9 sm:!h-8">
                <TabsTrigger value="day">{tRange("day")}</TabsTrigger>
                <TabsTrigger value="hour">{tRange("hour")}</TabsTrigger>
              </TabsList>
            </Tabs>
          ) : null}
          <div
            data-slot="header-actions"
            className={showGranularity
              ? "flex items-center gap-2 sm:order-last"
              : "col-start-2 row-start-1 flex items-center gap-2 sm:order-last"
            }
          >
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="outline"
                    size="icon-sm"
                    className="size-9 sm:size-8"
                    onClick={onRefresh}
                    disabled={refreshing}
                    aria-label="Refresh"
                  >
                    <RefreshCw className={refreshing ? "animate-spin" : ""} />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{tb("refresh")}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
          <DateRangePicker
            value={{ startDate: startStr, endDate: endStr }}
            onValueChange={handleDateRangeChange}
            placeholder={tb("dateRange")}
            size="sm"
            maxDays={maxDays}
            className={showGranularity
              ? "col-span-2 w-full [&_[data-slot=date-range-trigger]]:h-9 sm:w-auto sm:[&_[data-slot=date-range-trigger]]:h-8"
              : "col-start-1 row-start-1 min-w-0 w-full [&_[data-slot=date-range-trigger]]:h-9 sm:w-auto sm:[&_[data-slot=date-range-trigger]]:h-8"
            }
          />
        </div>
      </div>
      {scopeControls && (
        <div data-slot="page-scope-rail" className="flex flex-col gap-2">
          <Separator />
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            {scopeLabel && (
              <span className="shrink-0 text-sm text-muted-foreground">{scopeLabel}</span>
            )}
            <div className="grid min-w-0 grid-cols-2 gap-2 [&>*]:min-w-0 [&>*:last-child:nth-child(odd)]:col-span-2 [&_button[role=combobox]]:h-9 [&_[data-slot=select-trigger]]:h-9 sm:flex sm:flex-wrap sm:items-center sm:[&_button[role=combobox]]:h-8 sm:[&_[data-slot=select-trigger]]:h-8">
              {scopeControls}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
