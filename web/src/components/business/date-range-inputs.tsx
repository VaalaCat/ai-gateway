"use client";

import { useTranslations } from "next-intl";

import { Label } from "@/components/ui/label";
import { DatePicker, parseDateStr } from "@/components/business/date-picker/date-picker";
import { cn } from "@/lib/utils";

interface DateRangeInputsProps {
  startDate: string;
  endDate: string;
  onStartDateChange: (date: string) => void;
  onEndDateChange: (date: string) => void;
  labels?: { start?: string; end?: string };
  /** 紧凑模式：不渲染 Label，用 placeholder 表达字段含义（toolbar 内用）。 */
  compact?: boolean;
}

export function DateRangeInputs({
  startDate,
  endDate,
  onStartDateChange,
  onEndDateChange,
  labels,
  compact,
}: DateRangeInputsProps) {
  const tc = useTranslations("common");
  const tb = useTranslations("billing");

  const startParsed = parseDateStr(startDate);
  const endParsed = parseDateStr(endDate);
  const isInvalid = !!(startDate && endDate && startDate > endDate);

  const startLabel = labels?.start ?? tb("startDate");
  const endLabel = labels?.end ?? tb("endDate");

  return (
    <div className={cn(compact ? "" : "space-y-2")}>
      <div
        className={cn(
          "flex flex-col gap-3 sm:flex-row",
          compact ? "sm:items-center sm:gap-2" : "sm:items-end sm:gap-4",
        )}
      >
        {compact ? (
          <DatePicker
            value={startDate}
            onChange={onStartDateChange}
            placeholder={startLabel}
            disabledRange={endParsed ? { after: endParsed } : undefined}
          />
        ) : (
          <div className="space-y-1">
            <Label>{startLabel}</Label>
            <DatePicker
              value={startDate}
              onChange={onStartDateChange}
              placeholder={tc("selectDate")}
              disabledRange={endParsed ? { after: endParsed } : undefined}
            />
          </div>
        )}
        {compact ? (
          <DatePicker
            value={endDate}
            onChange={onEndDateChange}
            placeholder={endLabel}
            disabledRange={startParsed ? { before: startParsed } : undefined}
          />
        ) : (
          <div className="space-y-1">
            <Label>{endLabel}</Label>
            <DatePicker
              value={endDate}
              onChange={onEndDateChange}
              placeholder={tc("selectDate")}
              disabledRange={startParsed ? { before: startParsed } : undefined}
            />
          </div>
        )}
      </div>
      {isInvalid && !compact && (
        <p className="text-sm text-destructive">{tc("dateRangeError")}</p>
      )}
    </div>
  );
}

export function isDateRangeValid(startDate: string, endDate: string): boolean {
  if (startDate && endDate && startDate > endDate) return false;
  return true;
}
