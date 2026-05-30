"use client";

import { cn } from "@/lib/utils";

import { DatePicker, parseDateStr } from "./date-picker";

interface DateRangePickerProps {
  startDate: string;
  endDate: string;
  onStartDateChange: (date: string) => void;
  onEndDateChange: (date: string) => void;
  startPlaceholder?: string;
  endPlaceholder?: string;
  disabled?: boolean;
  className?: string;
}

export function DateRangePicker({
  startDate,
  endDate,
  onStartDateChange,
  onEndDateChange,
  startPlaceholder,
  endPlaceholder,
  disabled,
  className,
}: DateRangePickerProps) {
  const startParsed = parseDateStr(startDate);
  const endParsed = parseDateStr(endDate);
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      <DatePicker
        value={startDate}
        onChange={onStartDateChange}
        placeholder={startPlaceholder}
        disabledRange={endParsed ? { after: endParsed } : undefined}
        disabled={disabled}
      />
      <DatePicker
        value={endDate}
        onChange={onEndDateChange}
        placeholder={endPlaceholder}
        disabledRange={startParsed ? { before: startParsed } : undefined}
        disabled={disabled}
      />
    </div>
  );
}
