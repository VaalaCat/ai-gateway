"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Check, ChevronsUpDown, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { useUsers, useUser } from "@/lib/api/users";
import { useDebounce } from "@/hooks/use-debounce";
import { cn } from "@/lib/utils";

interface UserPickerProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  disabled?: boolean;
}

const PAGE_SIZE = 50;

export function UserPicker({
  value,
  onChange,
  placeholder,
  className,
  disabled,
}: UserPickerProps) {
  const t = useTranslations("userPicker");
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounce(query, 300);

  const selectedId = value ? Number(value) : 0;
  const { data: selectedUser } = useUser(selectedId);

  const { data: list } = useUsers(
    { page: 1, page_size: PAGE_SIZE, search: debouncedQuery },
    { enabled: open }
  );
  const users = list?.data ?? [];
  const showMoreHint = users.length === PAGE_SIZE;

  const isPureDigits = query.length > 0 && /^\d+$/.test(query);

  const handleSelect = (id: string) => {
    onChange(id);
    setOpen(false);
  };

  const handleClear = (e: React.MouseEvent) => {
    e.stopPropagation();
    onChange("");
  };

  const triggerLabel = (() => {
    if (!value) return placeholder ?? t("all");
    if (selectedUser?.username) {
      return (
        <span className="truncate">
          {selectedUser.username}
          <span className="text-muted-foreground ml-1">#{value}</span>
        </span>
      );
    }
    return <span className="text-muted-foreground">#{value}</span>;
  })();

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-haspopup="listbox"
          disabled={disabled}
          className={cn(
            "justify-between font-normal",
            !value && "text-muted-foreground",
            className,
          )}
        >
          <span className="truncate">{triggerLabel}</span>
          {value ? (
            <X
              className="ml-2 size-4 shrink-0 opacity-50 hover:opacity-100"
              onClick={handleClear}
              aria-label={t("clear")}
            />
          ) : (
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
        <Command shouldFilter={false}>
          <CommandInput
            placeholder={t("search")}
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            {users.length === 0 ? (
              <CommandEmpty>
                {isPureDigits ? (
                  <button
                    type="button"
                    className="w-full text-left hover:underline"
                    onClick={() => handleSelect(query)}
                  >
                    {t("useId", { id: query })}
                  </button>
                ) : (
                  t("empty")
                )}
              </CommandEmpty>
            ) : (
              <>
                {users.map((u) => (
                  <CommandItem
                    key={u.id}
                    value={String(u.id)}
                    onSelect={() => handleSelect(String(u.id))}
                  >
                    <Check
                      className={cn(
                        "mr-2 size-4",
                        value === String(u.id) ? "opacity-100" : "opacity-0",
                      )}
                    />
                    <span className="truncate">{u.username}</span>
                    <span className="text-muted-foreground ml-2">#{u.id}</span>
                  </CommandItem>
                ))}
                {showMoreHint && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    {t("moreHint")}
                  </div>
                )}
              </>
            )}
          </CommandList>
          {value && (
            <>
              <CommandSeparator />
              <div className="p-1">
                <button
                  type="button"
                  className="w-full rounded-sm px-2 py-1.5 text-sm text-left hover:bg-accent"
                  onClick={() => handleSelect("")}
                >
                  {t("clear")}
                </button>
              </div>
            </>
          )}
        </Command>
      </PopoverContent>
    </Popover>
  );
}
