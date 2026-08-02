"use client";

import { CalendarIcon, X } from "lucide-react";
import { format, isValid, parse } from "date-fns";
import { useTranslations } from "next-intl";
import { type DateAfter, type DateBefore } from "react-day-picker";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const FMT = "yyyy-MM-dd";

export function parseDateStr(value: string): Date | undefined {
  if (!value) return undefined;
  const parsed = parse(value, FMT, new Date());
  return isValid(parsed) && format(parsed, FMT) === value ? parsed : undefined;
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
  /** sm = 工具栏紧凑档(h-8, sm:w-[150px]);默认档不变。 */
  size?: "sm" | "default";
}

export function DatePicker({
  value,
  onChange,
  placeholder,
  disabledRange,
  disabled,
  className,
  size = "default",
}: DatePickerProps) {
  const t = useTranslations("common");
  const selected = parseDateStr(value);
  return (
    <div
      className={cn(
        "relative w-full sm:w-[160px]",
        size === "sm" && "sm:w-[150px]",
        className,
      )}
    >
      <Popover>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            disabled={disabled}
            className={cn(
              "w-full justify-start text-left font-normal text-body",
              size === "sm" && "h-8",
              selected && "pr-9",
              !selected && "text-muted-foreground",
            )}
          >
            <CalendarIcon className="mr-2 size-4" />
            <span className="truncate">
              {selected ? formatDateStr(selected) : (placeholder ?? "")}
            </span>
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
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("clearDate")}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            onChange("");
          }}
          className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
        >
          <X />
        </Button>
      )}
    </div>
  );
}
