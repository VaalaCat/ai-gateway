"use client";

import { CalendarIcon, X } from "lucide-react";
import { format, parse } from "date-fns";
import { useTranslations } from "next-intl";

import { type DateAfter, type DateBefore } from "react-day-picker";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
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

function parseDate(value: string): Date | undefined {
  if (!value) return undefined;
  return parse(value, "yyyy-MM-dd", new Date());
}

function formatDateStr(date: Date): string {
  return format(date, "yyyy-MM-dd");
}

function DatePicker({
  value,
  onChange,
  label,
  placeholder,
  disabled,
  compact,
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  placeholder: string;
  disabled?: DateBefore | DateAfter;
  compact?: boolean;
}) {
  const selected = parseDate(value);

  const trigger = (
    <div className="flex items-center gap-1">
      <Popover>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            className={cn(
              "w-full sm:w-[160px] justify-start text-left font-normal text-body",
              !selected && "text-muted-foreground"
            )}
          >
            <CalendarIcon className="mr-2 size-4" />
            {selected ? format(selected, "yyyy-MM-dd") : compact ? label : placeholder}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="start">
          <Calendar
            mode="single"
            selected={selected}
            onSelect={(day) => onChange(day ? formatDateStr(day) : "")}
            disabled={disabled}
            autoFocus
          />
        </PopoverContent>
      </Popover>
      {selected && (
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={() => onChange("")}
          className="text-muted-foreground"
        >
          <X />
        </Button>
      )}
    </div>
  );

  if (compact) return trigger;
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {trigger}
    </div>
  );
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

  const startParsed = parseDate(startDate);
  const endParsed = parseDate(endDate);
  const isInvalid = !!(startDate && endDate && startDate > endDate);

  return (
    <div className={cn(compact ? "" : "space-y-2")}>
      <div
        className={cn(
          "flex flex-col gap-3 sm:flex-row",
          compact ? "sm:items-center sm:gap-2" : "sm:items-end sm:gap-4",
        )}
      >
        <DatePicker
          value={startDate}
          onChange={onStartDateChange}
          label={labels?.start ?? tb("startDate")}
          placeholder={tc("selectDate")}
          disabled={endParsed ? { after: endParsed } : undefined}
          compact={compact}
        />
        <DatePicker
          value={endDate}
          onChange={onEndDateChange}
          label={labels?.end ?? tb("endDate")}
          placeholder={tc("selectDate")}
          disabled={startParsed ? { before: startParsed } : undefined}
          compact={compact}
        />
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
