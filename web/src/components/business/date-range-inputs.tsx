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
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  placeholder: string;
  disabled?: DateBefore | DateAfter;
}) {
  const selected = parseDate(value);

  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <div className="flex items-center gap-1">
        <Popover>
          <PopoverTrigger asChild>
            <Button
              variant="outline"
              className={cn(
                "w-[160px] justify-start text-left font-normal",
                !selected && "text-muted-foreground"
              )}
            >
              <CalendarIcon className="mr-2 size-4" />
              {selected ? format(selected, "yyyy-MM-dd") : placeholder}
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
    </div>
  );
}

export function DateRangeInputs({
  startDate,
  endDate,
  onStartDateChange,
  onEndDateChange,
  labels,
}: DateRangeInputsProps) {
  const tc = useTranslations("common");
  const tb = useTranslations("billing");

  const startParsed = parseDate(startDate);
  const endParsed = parseDate(endDate);
  const isInvalid = !!(startDate && endDate && startDate > endDate);

  return (
    <div className="space-y-2">
      <div className="flex items-end gap-4">
        <DatePicker
          value={startDate}
          onChange={onStartDateChange}
          label={labels?.start ?? tb("startDate")}
          placeholder={tc("selectDate")}
          disabled={endParsed ? { after: endParsed } : undefined}
        />
        <DatePicker
          value={endDate}
          onChange={onEndDateChange}
          label={labels?.end ?? tb("endDate")}
          placeholder={tc("selectDate")}
          disabled={startParsed ? { before: startParsed } : undefined}
        />
      </div>
      {isInvalid && (
        <p className="text-sm text-destructive">{tc("dateRangeError")}</p>
      )}
    </div>
  );
}

export function isDateRangeValid(startDate: string, endDate: string): boolean {
  if (startDate && endDate && startDate > endDate) return false;
  return true;
}
