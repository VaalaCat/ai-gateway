"use client";

import { useState } from "react";
import { CalendarIcon, X } from "lucide-react";
import { format } from "date-fns";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

interface DateTimePickerProps {
  value: number | null; // unix 秒(本地时区)
  onChange: (value: number | null) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
}

export function DateTimePicker({
  value,
  onChange,
  placeholder,
  disabled,
  className,
}: DateTimePickerProps) {
  const [open, setOpen] = useState(false);
  const date = value ? new Date(value * 1000) : undefined;
  const timeStr = date ? format(date, "HH:mm") : "00:00";
  const commit = (d: Date) => onChange(Math.floor(d.getTime() / 1000));

  const onPickDay = (day: Date | undefined) => {
    if (!day) return onChange(null);
    const base = date ?? new Date();
    const next = new Date(day); // 克隆:勿改写 react-day-picker 内部持有的 Date
    next.setHours(base.getHours(), base.getMinutes(), 0, 0);
    commit(next);
  };
  const onPickTime = (hhmm: string) => {
    const [h, m] = hhmm.split(":").map(Number);
    const base = date ? new Date(date) : new Date();
    base.setHours(h || 0, m || 0, 0, 0);
    commit(base);
  };

  return (
    <div className={cn("flex items-center gap-1", className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            disabled={disabled}
            className={cn(
              "w-full justify-start text-left font-normal text-body",
              !date && "text-muted-foreground",
            )}
          >
            <CalendarIcon className="mr-2 size-4" />
            {date ? format(date, "yyyy-MM-dd HH:mm") : (placeholder ?? "")}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="start">
          <Calendar mode="single" selected={date} onSelect={onPickDay} autoFocus />
          <div className="border-t p-2">
            <Input type="time" value={timeStr} onChange={(e) => onPickTime(e.target.value)} />
          </div>
        </PopoverContent>
      </Popover>
      {date && !disabled && (
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={() => onChange(null)}
          className="text-muted-foreground"
        >
          <X />
        </Button>
      )}
    </div>
  );
}
