"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Check, ChevronsUpDown, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { cn } from "@/lib/utils";
import { EntityLabel } from "@/components/business/entity-label";
import { ENTITY_ADAPTERS, type EntityName } from "./registry";
import type { EntityAdapter } from "./types";
import { useEntityOptions } from "./use-entity-options";

const PAGE_SIZE = 50;

interface EntityMultiPickerProps {
  entity: EntityName;
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  /** 从下拉可选项中排除的 value（如已绑定项）。默认不排除，向后兼容。 */
  excludeIds?: string[];
}

export function EntityMultiPicker({
  entity,
  value,
  onChange,
  placeholder,
  disabled,
  className,
  excludeIds,
}: EntityMultiPickerProps) {
  const t = useTranslations("entityPicker");
  const adapter = ENTITY_ADAPTERS[entity] as unknown as EntityAdapter<unknown>;

  const [open, setOpen] = useState(false);
  const { search, setSearch, items, isLoading, getValue, renderItem } =
    useEntityOptions(adapter, { scope: "self", pageSize: PAGE_SIZE });

  const excludeSet = new Set(excludeIds ?? []);
  const visibleItems = items.filter((it) => !excludeSet.has(getValue(it)));

  const selectedSet = new Set(value);
  const toggle = (v: string) =>
    onChange(selectedSet.has(v) ? value.filter((x) => x !== v) : [...value, v]);

  const placeholderText = placeholder || t(`placeholder.${entity}` as never) || "";

  return (
    <div className={cn("space-y-2", className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className="w-full justify-between font-normal text-body"
          >
            <span className={cn("truncate", value.length === 0 && "text-muted-foreground")}>
              {value.length === 0 ? placeholderText : t("selectedCount", { count: value.length })}
            </span>
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={t("searchPlaceholder")}
              value={search}
              onValueChange={setSearch}
            />
            <CommandList>
              {isLoading ? (
                <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                  {t("loading")}
                </div>
              ) : visibleItems.length === 0 ? (
                <CommandEmpty>{t("noResults")}</CommandEmpty>
              ) : (
                visibleItems.map((item) => {
                  const v = getValue(item);
                  return (
                    <CommandItem key={v} value={v} onSelect={() => toggle(v)}>
                      <Check
                        className={cn(
                          "mr-2 size-4",
                          selectedSet.has(v) ? "opacity-100" : "opacity-0",
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
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {value.map((v) => (
            <Badge
              key={v}
              variant="secondary"
              className="cursor-pointer"
              onClick={() => !disabled && toggle(v)}
            >
              <EntityLabel entity={entity} id={v} showId={false} hover={false} className="truncate" />
              <X className="ml-1 size-3" />
            </Badge>
          ))}
        </div>
      )}
    </div>
  );
}
