"use client";

import { CalendarIcon, X } from "lucide-react";
import { format, parse } from "date-fns";
import { type DateAfter, type DateBefore } from "react-day-picker";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const FMT = "yyyy-MM-dd";

export function parseDateStr(value: string): Date | undefined {
  if (!value) return undefined;
  return parse(value, FMT, new Date());
}
export function formatDateStr(date: Date): string {
  return format(date, FMT);
}

interface DatePickerProps {
  value: string; // yyyy-MM-dd
  onChange: (value: string) => void;
  placeholder?: string;
  disabledRange?: DateBefore | DateAfter;
  disabled?: boolean;
  className?: string;
}

export function DatePicker({
  value,
  onChange,
  placeholder,
  disabledRange,
  disabled,
  className,
}: DatePickerProps) {
  const selected = parseDateStr(value);
  return (
    <div className={cn("flex items-center gap-1", className)}>
      <Popover>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            disabled={disabled}
            className={cn(
              "w-full sm:w-[160px] justify-start text-left font-normal text-body",
              !selected && "text-muted-foreground",
            )}
          >
            <CalendarIcon className="mr-2 size-4" />
            {selected ? format(selected, FMT) : (placeholder ?? "")}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="start">
          <Calendar
            mode="single"
            selected={selected}
            onSelect={(d) => onChange(d ? formatDateStr(d) : "")}
            disabled={disabledRange}
            autoFocus
          />
        </PopoverContent>
      </Popover>
      {selected && !disabled && (
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
}
