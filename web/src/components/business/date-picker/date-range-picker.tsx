"use client";

import { useId, useState } from "react";
import { addDays } from "date-fns";
import { CalendarIcon, X } from "lucide-react";
import { useTranslations } from "next-intl";
import type { DateRange, Matcher } from "react-day-picker";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Field, FieldLabel } from "@/components/ui/field";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { formatDateStr, parseDateStr } from "./date-picker";

export interface DateRangeValue {
  startDate: string;
  endDate: string;
}

export interface DateRangePickerProps {
  value: DateRangeValue;
  onValueChange: (value: DateRangeValue) => void;
  label?: string;
  placeholder?: string;
  size?: "sm" | "default";
  maxDays?: number;
  disabled?: boolean;
  className?: string;
}

export function isDateRangeValid(value: DateRangeValue): boolean {
  if (!value.startDate && !value.endDate) return true;
  const start = parseDateStr(value.startDate);
  const end = parseDateStr(value.endDate);
  return Boolean(start && end && start <= end);
}

function toDateRange(value: DateRangeValue): DateRange | undefined {
  if (!value.startDate || !value.endDate || !isDateRangeValid(value)) return undefined;
  return { from: parseDateStr(value.startDate), to: parseDateStr(value.endDate) };
}

function dateRangeValueKey(value: DateRangeValue): string {
  return `${value.startDate}:${value.endDate}`;
}

interface SelectionSession {
  anchor: Date;
  draft: DateRange;
  expectedControlledValueKey?: string;
}

export function DateRangePicker({
  value,
  onValueChange,
  label,
  placeholder,
  size = "default",
  maxDays,
  disabled,
  className,
}: DateRangePickerProps) {
  const t = useTranslations("common");
  const isMobile = useIsMobile();
  const triggerId = useId();
  const controlledRange = toDateRange(value);
  const controlledValueKey = dateRangeValueKey(value);
  const [open, setOpen] = useState(false);
  const [lastControlledValueKey, setLastControlledValueKey] = useState(controlledValueKey);
  const [selectionSession, setSelectionSession] = useState<SelectionSession>();

  if (controlledValueKey !== lastControlledValueKey) {
    setLastControlledValueKey(controlledValueKey);
    setSelectionSession(
      selectionSession?.expectedControlledValueKey === controlledValueKey
        ? { ...selectionSession, expectedControlledValueKey: undefined }
        : undefined,
    );
  }

  const rangeLimit =
    typeof maxDays === "number" && Number.isFinite(maxDays)
      ? Math.max(1, Math.floor(maxDays))
      : undefined;
  const disabledDays: Matcher[] | undefined =
    selectionSession && rangeLimit
      ? [
          { before: addDays(selectionSession.anchor, -(rangeLimit - 1)) },
          { after: addDays(selectionSession.anchor, rangeLimit - 1) },
        ]
      : undefined;

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (nextOpen) {
      setSelectionSession(undefined);
    }
  };

  const handleSelect = (selectedDay: Date) => {
    if (!selectionSession) {
      const sameDay = { from: selectedDay, to: selectedDay };
      const nextValue = {
        startDate: formatDateStr(selectedDay),
        endDate: formatDateStr(selectedDay),
      };
      setSelectionSession({
        anchor: selectedDay,
        draft: sameDay,
        expectedControlledValueKey: dateRangeValueKey(nextValue),
      });
      // behavior change: the first click now emits a complete same-day range.
      onValueChange(nextValue);
      return;
    }

    const from = selectedDay < selectionSession.anchor ? selectedDay : selectionSession.anchor;
    const to = selectedDay < selectionSession.anchor ? selectionSession.anchor : selectedDay;
    setSelectionSession(undefined);
    onValueChange({ startDate: formatDateStr(from), endDate: formatDateStr(to) });
    setOpen(false);
  };

  const clear = () => {
    setSelectionSession(undefined);
    setOpen(false);
    onValueChange({ startDate: "", endDate: "" });
  };

  const control = (
    <div className={cn("relative w-full sm:w-[280px]", className)}>
      <Popover open={open} onOpenChange={handleOpenChange}>
        <PopoverTrigger asChild>
          <Button
            id={triggerId}
            data-slot="date-range-trigger"
            variant="outline"
            disabled={disabled}
            className={cn(
              "w-full justify-start text-left font-normal text-body",
              size === "sm" && "h-8",
              !controlledRange && "text-muted-foreground",
            )}
          >
            <CalendarIcon className="mr-2 size-4" />
            <span className={cn("min-w-0 flex-1 truncate", controlledRange && "pr-7")}>
              {controlledRange?.from && controlledRange.to
                ? `${formatDateStr(controlledRange.from)} - ${formatDateStr(controlledRange.to)}`
                : (placeholder ?? t("selectDate"))}
            </span>
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          className="max-w-[calc(100vw-2rem)] w-auto overflow-auto p-0"
        >
          <Calendar
            mode="range"
            selected={selectionSession?.draft ?? controlledRange}
            defaultMonth={controlledRange?.from}
            onSelect={(_range, selectedDay) => handleSelect(selectedDay)}
            disabled={disabledDays}
            numberOfMonths={isMobile ? 1 : 2}
            autoFocus
          />
        </PopoverContent>
      </Popover>
      {controlledRange && !disabled && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("clearDateRange")}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            clear();
          }}
          className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
        >
          <X />
        </Button>
      )}
    </div>
  );

  if (!label) return control;
  return (
    <Field>
      <FieldLabel htmlFor={triggerId}>{label}</FieldLabel>
      {control}
    </Field>
  );
}
