"use client";

import { useState, useMemo } from "react";
import { Check, ChevronsUpDown, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { useChannels } from "@/lib/api/channels";

interface ChannelMultiSelectProps {
  value: number[];
  onChange: (ids: number[]) => void;
  placeholder?: string;
  disabled?: boolean;
}

export function ChannelMultiSelect({ value, onChange, placeholder, disabled }: ChannelMultiSelectProps) {
  const [open, setOpen] = useState(false);
  const { data } = useChannels({ pageSize: 1000 });
  const channels = data?.data ?? [];

  const selectedSet = useMemo(() => new Set(value), [value]);

  const toggle = (id: number) => {
    const next = selectedSet.has(id) ? value.filter((v) => v !== id) : [...value, id];
    onChange(next);
  };

  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="outline" className="w-full justify-between" disabled={disabled}>
            <span>{value.length === 0 ? (placeholder ?? "Select channels...") : `${value.length} selected`}</span>
            <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-0" align="start">
          <Command>
            <CommandInput placeholder={placeholder} />
            <CommandList>
              <CommandEmpty>No channel found.</CommandEmpty>
              <CommandGroup>
                {channels.map((ch) => (
                  <CommandItem key={ch.id} value={`${ch.id} ${ch.name} ${ch.tag ?? ""}`} onSelect={() => toggle(ch.id)}>
                    <Check
                      className={cn(
                        "mr-2 h-4 w-4",
                        selectedSet.has(ch.id) ? "opacity-100" : "opacity-0",
                      )}
                    />
                    <span>#{ch.id} {ch.name}</span>
                    {ch.tag && <span className="ml-2 text-xs text-muted-foreground">[{ch.tag}]</span>}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {value.map((id) => {
            const ch = channels.find((c) => c.id === id);
            return (
              <Badge key={id} variant="secondary" className="cursor-pointer" onClick={() => !disabled && toggle(id)}>
                {ch ? `#${ch.id} ${ch.name}` : `#${id}`}
                <X className="ml-1 h-3 w-3" />
              </Badge>
            );
          })}
        </div>
      )}
    </div>
  );
}
