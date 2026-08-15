"use client";

import { useEffect, useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { cn } from "@/lib/utils";

export interface SearchableSelectItem {
  value: string;
  label: string;
}

export interface SearchableSelectProps {
  value: string;
  onChange: (value: string) => void;
  ariaLabel?: string;
  placeholder: string;
  searchPlaceholder: string;
  items: SearchableSelectItem[];
  className?: string;
  disabled?: boolean;
  emptyText?: string;
  selectedLabel?: string;
  loading?: boolean;
  remoteSearch?: {
    value: string;
    onCommit: (value: string) => void;
    debounceMs?: number;
  };
}

export function SearchableSelect({
  value,
  onChange,
  ariaLabel,
  placeholder,
  searchPlaceholder,
  items,
  className,
  disabled,
  emptyText = "No results",
  selectedLabel,
  loading = false,
  remoteSearch,
}: SearchableSelectProps) {
  const [open, setOpen] = useState(false);
  const selected = items.find((i) => i.value === value);
  const displayLabel = selected?.label || (value ? selectedLabel : undefined);
  const remoteValue = remoteSearch?.value;
  const remoteCommit = remoteSearch?.onCommit;
  const remoteDebounceMs = remoteSearch?.debounceMs ?? 300;
  const [remoteDraftState, setRemoteDraftState] = useState(() => ({
    source: remoteValue,
    value: remoteValue ?? "",
  }));
  const remoteDraft = remoteDraftState.source === remoteValue
    ? remoteDraftState.value
    : remoteValue ?? "";

  useEffect(() => {
    if (!remoteCommit || remoteDraft === remoteValue) return;
    const timer = window.setTimeout(
      () => remoteCommit(remoteDraft),
      remoteDebounceMs,
    );
    return () => window.clearTimeout(timer);
  }, [remoteCommit, remoteDebounceMs, remoteDraft, remoteValue]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-label={ariaLabel}
          aria-expanded={open}
          aria-haspopup="listbox"
          disabled={disabled}
          className={cn(
            "w-full justify-between h-8 text-sm font-normal",
            !value && "text-muted-foreground",
            className,
          )}
        >
          <span className="truncate">{displayLabel || placeholder}</span>
          <ChevronsUpDown className="ml-1 size-3.5 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[--radix-popover-trigger-width] p-0"
        align="start"
      >
        <Command shouldFilter={!remoteSearch}>
          <CommandInput
            placeholder={searchPlaceholder}
            className="h-8 text-sm"
            value={remoteSearch ? remoteDraft : undefined}
            onValueChange={remoteSearch
              ? (nextValue) => setRemoteDraftState({ source: remoteValue, value: nextValue })
              : undefined}
          />
          <CommandList>
            {loading ? (
              <div role="status" className="px-3 py-6 text-center text-sm text-muted-foreground">
                {searchPlaceholder}
              </div>
            ) : <CommandEmpty>{emptyText}</CommandEmpty>}
            <CommandGroup>
              {items.map((item) => (
                <CommandItem
                  key={item.value}
                  value={item.label}
                  onSelect={() => {
                    onChange(item.value);
                    setOpen(false);
                  }}
                  className="text-sm"
                >
                  <Check
                    className={cn(
                      "mr-1.5 size-3.5",
                      value === item.value ? "opacity-100" : "opacity-0",
                    )}
                  />
                  {item.label}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
