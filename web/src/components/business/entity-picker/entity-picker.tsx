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
const PICKER_HEIGHTS = {
  xs: "h-7",
  sm: "h-8",
  default: "h-full min-h-11",
} as const;

export interface EntityPickerProps {
  id?: string;
  entity: EntityName;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  disabled?: boolean;
  /** Compact toolbar sizes: xs = h-7, sm = h-8; default preserves h-full stretching. */
  size?: keyof typeof PICKER_HEIGHTS;
  /** Token 等 owner-scoped entity 的显式所有者；设置后优先于 self/all scope。 */
  ownerUserId?: number;
	/** 管理员首次打开时的候选范围；普通用户和显式 owner 始终不受它影响。 */
	defaultAdminScope?: AdminScope;
	/** 限制候选/已选值读取到指定 API Service 的子资源。 */
	apiServiceId?: number;
	/** 与 apiServiceId 一起限制可 invoke Token 的目标 API Route。 */
	apiRouteId?: number;
}

export function EntityPicker({
  id,
  entity,
  value,
  onChange,
  placeholder,
  className,
  disabled,
  size = "default",
  ownerUserId,
	defaultAdminScope,
	apiServiceId,
	apiRouteId,
}: EntityPickerProps) {
  const t = useTranslations("entityPicker");
  // Cast to EntityAdapter<unknown> so adapter methods work with a single unknown item type
  const adapter = ENTITY_ADAPTERS[entity] as unknown as EntityAdapter<unknown>;
  const { isAdmin } = useAuth();
  const showScope = Boolean(adapter.supportsAdminScope && isAdmin && ownerUserId === undefined);

  const [open, setOpen] = useState(false);
	const initialScope: AdminScope = isAdmin ? (defaultAdminScope ?? "self") : "self";
  const [scope, setScope] = useState<AdminScope>(initialScope);
	const [scopeIsAdmin, setScopeIsAdmin] = useState(isAdmin);
	if (scopeIsAdmin !== isAdmin) {
		setScopeIsAdmin(isAdmin);
		setScope(isAdmin ? (defaultAdminScope ?? "self") : "self");
	}
  const [wasDisabled, setWasDisabled] = useState(Boolean(disabled));
  if (Boolean(disabled) !== wasDisabled) {
    setWasDisabled(Boolean(disabled));
    if (disabled && open) setOpen(false);
  }
  const isOpen = open && !disabled;
	const normalizedScope: AdminScope = isAdmin ? scope : "self";

  const {
    search,
    setSearch,
    items,
    isLoading,
    isError,
    refetch,
    getValue,
    renderItem,
  } = useEntityOptions(adapter, {
    scope: normalizedScope,
    pageSize: PAGE_SIZE,
    ownerUserId,
		apiServiceId,
		apiRouteId,
		enabled: open && !disabled,
	});
	const one = adapter.useOne(value, { scope: normalizedScope, ownerUserId, apiServiceId, apiRouteId });
  const selectedLabel = one.data
    ? adapter.getLabel(one.data)
    : (adapter.labelForValue?.(value) ?? "");
  // Fallback placeholder: i18n placeholder.<entity-name> then prop then empty
  const placeholderText =
    placeholder || t(`placeholder.${entity}` as never) || "";
  const displayLabel = selectedLabel || placeholderText;
	const selectedValueError = Boolean(value && one.isError);

  const handleSelect = (v: string) => {
    if (disabled) return;
    onChange(v);
    setOpen(false);
  };

  const handleClear = () => {
    onChange("");
  };

  return (
    <div
      data-slot="entity-picker"
      data-state={disabled ? "disabled" : open ? "open" : "closed"}
      className={cn("relative", className)}
    >
      <Popover
        open={isOpen}
        onOpenChange={(nextOpen) => setOpen(disabled ? false : nextOpen)}
      >
        <PopoverTrigger asChild>
          <Button
            id={id}
            variant="outline"
            role="combobox"
            aria-expanded={isOpen}
            disabled={disabled}
            className={cn(
              "w-full justify-between font-normal text-body",
              PICKER_HEIGHTS[size],
            )}
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
              ) : isError ? (
                <div
                  data-slot="entity-picker-error"
                  data-state="error"
                  className="flex flex-col items-center gap-2 px-3 py-6 text-center text-sm text-muted-foreground"
                >
                  <span>{t("loadFailed")}</span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    data-slot="entity-picker-retry"
                    data-state="error"
					onClick={() => void refetch()}
                  >
                    {t("retry")}
                  </Button>
                </div>
              ) : (
                <>
					{selectedValueError && (
						<div
							data-slot="entity-picker-selected-error"
							data-state="error"
							className="flex items-center justify-between gap-2 border-b px-3 py-2 text-sm text-muted-foreground"
						>
							<span>{t("loadFailed")}</span>
							<Button
								type="button"
								variant="outline"
								size="sm"
								data-slot="entity-picker-selected-retry"
								data-state="error"
								onClick={() => void one.refetch()}
							>
								{t("retry")}
							</Button>
						</div>
					)}
					{items.length === 0 ? (
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
				</>
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
          className={cn("absolute right-9 top-1/2 -translate-y-1/2 flex items-center justify-center text-muted-foreground hover:text-foreground", size === "default" && "min-h-11 min-w-11")}
        >
          <X className="size-4 opacity-50 hover:opacity-100" />
        </button>
      )}
    </div>
  );
}
