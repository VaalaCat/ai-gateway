"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Check, ChevronsUpDown, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { AdminScopeToggle } from "@/components/business/admin-scope-toggle";
import { useAuth } from "@/lib/auth";
import { cn } from "@/lib/utils";
import { useEntityOptions } from "./use-entity-options";
import { ENTITY_ADAPTERS, type EntityName } from "./registry";
import type { AdminScope, EntityAdapter } from "./types";

const PAGE_SIZE = 50;

interface EntityPickerProps {
  entity: EntityName;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  disabled?: boolean;
}

export function EntityPicker({
  entity,
  value,
  onChange,
  placeholder,
  className,
  disabled,
}: EntityPickerProps) {
  const t = useTranslations("entityPicker");
  // Cast to EntityAdapter<unknown> so adapter methods work with a single unknown item type
  const adapter = ENTITY_ADAPTERS[entity] as unknown as EntityAdapter<unknown>;
  const { isAdmin } = useAuth();
  const showScope = Boolean(adapter.supportsAdminScope && isAdmin);

  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<AdminScope>("self");

  const { search, setSearch, items, isLoading, getValue, renderItem } =
    useEntityOptions(adapter, { scope, pageSize: PAGE_SIZE });
  const one = adapter.useOne(value, { scope });
  const selectedLabel = one.data
    ? adapter.getLabel(one.data)
    : (adapter.labelForValue?.(value) ?? "");
  // Fallback placeholder: i18n placeholder.<entity-name> then prop then empty
  const placeholderText =
    placeholder || t(`placeholder.${entity}` as never) || "";
  const displayLabel = selectedLabel || placeholderText;

  const handleSelect = (v: string) => {
    onChange(v);
    setOpen(false);
  };

  const handleClear = () => {
    onChange("");
  };

  return (
    <div className={cn("relative", className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className="w-full h-full justify-between font-normal text-body"
          >
            <span
              className={cn(
                "truncate",
                !selectedLabel && "text-muted-foreground",
                value && !disabled && "pr-6",
              )}
            >
              {displayLabel}
            </span>
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-[--radix-popover-trigger-width] p-0">
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={t("searchPlaceholder")}
              value={search}
              onValueChange={setSearch}
            />
            {showScope && (
              <>
                <div className="px-2 py-2">
                  <AdminScopeToggle value={scope === "all" ? "global" : "self"} onChange={(v) => setScope(v === "global" ? "all" : "self")} />
                </div>
                <CommandSeparator />
              </>
            )}
            <CommandList>
              {isLoading ? (
                <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                  {t("loading")}
                </div>
              ) : items.length === 0 ? (
                <CommandEmpty>{t("noResults")}</CommandEmpty>
              ) : (
                items.map((item) => {
                  const itemValue = getValue(item);
                  return (
                    <CommandItem
                      key={itemValue}
                      value={itemValue}
                      onSelect={() => handleSelect(itemValue)}
                    >
                      <Check
                        className={cn(
                          "mr-2 size-4",
                          value === itemValue ? "opacity-100" : "opacity-0",
                        )}
                      />
                      {renderItem(item)}
                    </CommandItem>
                  );
                })
              )}
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {value && !disabled && (
        <button
          type="button"
          aria-label={t("clear")}
          onClick={handleClear}
          className="absolute right-9 top-1/2 -translate-y-1/2 flex items-center justify-center text-muted-foreground hover:text-foreground"
        >
          <X className="size-4 opacity-50 hover:opacity-100" />
        </button>
      )}
    </div>
  );
}
