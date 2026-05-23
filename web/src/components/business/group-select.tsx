"use client";

import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { Check, ChevronsUpDown } from "lucide-react";

import { Button } from "@/components/ui/button";
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
import { useUserGroupsAll } from "@/lib/api/user-groups";

interface GroupSelectProps {
  value?: number;
  onChange: (id: number) => void;
  disabled?: boolean;
  placeholder?: string;
}

export function GroupSelect({ value, onChange, disabled, placeholder }: GroupSelectProps) {
  const [open, setOpen] = useState(false);
  const tu = useTranslations("users");
  const { data, isLoading, isError, refetch } = useUserGroupsAll();
  const groups = data?.data ?? [];

  const selected = useMemo(
    () => groups.find((g) => g.id === value),
    [groups, value],
  );

  if (isError) {
    return (
      <Button
        type="button"
        variant="outline"
        className="w-full justify-between text-destructive"
        onClick={() => refetch()}
      >
        {tu("groupSelectLoadFailed")}
      </Button>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          disabled={disabled || isLoading}
          className="w-full justify-between text-body"
        >
          <span className={cn("truncate", !selected && "text-muted-foreground")}>
            {selected ? selected.name : placeholder ?? tu("groupPlaceholder")}
          </span>
          <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-0" align="start">
        <Command>
          <CommandInput placeholder={tu("groupPlaceholder")} />
          <CommandList>
            <CommandEmpty>{tu("groupEmpty")}</CommandEmpty>
            <CommandGroup>
              {groups.map((g) => (
                <CommandItem
                  key={g.id}
                  value={`${g.id} ${g.name}`}
                  onSelect={() => {
                    onChange(g.id);
                    setOpen(false);
                  }}
                >
                  <Check
                    className={cn(
                      "mr-2 h-4 w-4",
                      g.id === value ? "opacity-100" : "opacity-0",
                    )}
                  />
                  <span>{g.name}</span>
                  {g.description && (
                    <span className="ml-2 text-xs text-muted-foreground truncate">
                      {g.description}
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
