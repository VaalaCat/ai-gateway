"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { Loader2, MoreHorizontal, Search, SlidersHorizontal } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { FilterField } from "@/components/business/filter-bar";
import { DateRangePicker } from "@/components/business/date-picker/date-range-picker";
import { useDebounce } from "@/hooks/use-debounce";
import { useAuth } from "@/lib/auth";
import {
  buildCompleteDateRange,
  dateStrToTs,
  isFiniteUnixSeconds,
  tsToDateStr,
} from "@/lib/utils/date-range";

import type { FilterSpec, FilterValues, FilterContext } from "./filter-spec";
import type { ToolbarAction } from "./toolbar-actions";

const SECONDARY_COLLAPSE_THRESHOLD = 3;

function isActiveFilterValue(value: FilterValues[string]) {
  return value !== undefined && value !== "" && value !== 0;
}

interface FilterableToolbarProps {
  spec: FilterSpec;
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
  /** 自定义可见性上下文（与 isAdmin 合并）。 */
  context?: Partial<FilterContext>;
  primaryAction?: ReactNode;
  secondaryActions?: ToolbarAction[];
  /** 渲染在右侧 actions 区(secondary 按钮之前)的自定义控件,如自动刷新 Select。 */
  secondaryContent?: ReactNode;
  /** 桌面端筛选区独占一行，actions 在下一行右对齐。 */
  filtersOnOwnRow?: boolean;
}

export function FilterableToolbar({
  spec,
  value,
  onChange,
  context,
  primaryAction,
  secondaryActions,
  secondaryContent,
  filtersOnOwnRow = false,
}: FilterableToolbarProps) {
  const tc = useTranslations("common");
  const { isAdmin } = useAuth();
  const ctx: FilterContext = { isAdmin, ...context };

  const secondary = secondaryActions ?? [];
  const shouldCollapse = secondary.length >= SECONDARY_COLLAPSE_THRESHOLD;
  const entries = Object.entries(spec).filter(
    ([, def]) => !def.visible || def.visible(ctx),
  );
  const primary = entries.filter(([, def]) => !def.advanced);
  const advanced = entries.filter(([, def]) => Boolean(def.advanced));
  const activeAdvancedCount = advanced.filter(([key]) =>
    isActiveFilterValue(value[key]),
  ).length;
  const hasActions =
    advanced.length > 0 ||
    primaryAction !== undefined ||
    secondary.length > 0 ||
    secondaryContent !== undefined;
  const fitsTwoMobileRows = primary.length <= 3;

  return (
    <div className="grid grid-cols-2 gap-2 sm:flex sm:flex-row sm:flex-wrap sm:items-end sm:justify-between sm:gap-3 md:gap-4">
      {primary.length > 0 && (
        <div
          data-slot="toolbar-filters"
          className={cn(
            "contents min-w-0 flex-1 sm:flex sm:flex-row sm:flex-wrap sm:items-end",
            filtersOnOwnRow ? "sm:gap-2 md:basis-full" : "sm:gap-3",
          )}
        >
          {primary.map(([key, def]) => (
            <FilterControl
              key={key}
              fieldKey={key}
              def={def}
              value={value}
              onChange={onChange}
            />
          ))}
        </div>
      )}

      {hasActions && (
        <div
          data-slot="toolbar-actions"
          className={cn(
            "flex min-w-0 shrink-0 self-end flex-wrap items-center justify-end gap-2 sm:col-auto sm:row-auto",
            fitsTwoMobileRows && primary.length > 0 && "col-start-2 row-start-2",
            primary.length === 0 && "col-span-2",
            (filtersOnOwnRow || entries.length === 0) && "md:ml-auto",
          )}
        >
          {advanced.length > 0 && (
            <AdvancedFiltersPopover
              entries={advanced}
              value={value}
              onChange={onChange}
              count={activeAdvancedCount}
              label={tc("filters")}
            />
          )}
          {secondaryContent}
          {secondary.length > 0 && (
            <div className="sm:hidden">
              <ToolbarActionsMenu actions={secondary} label={tc("more")} />
            </div>
          )}
          <div className="hidden sm:contents">
            {shouldCollapse ? (
              <ToolbarActionsMenu actions={secondary} label={tc("more")} />
            ) : (
              secondary.map((a, i) => <ToolbarActionButton key={i} action={a} />)
            )}
          </div>
          {primaryAction}
        </div>
      )}
    </div>
  );
}

interface FilterControlProps {
  fieldKey: string;
  def: FilterSpec[string];
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
  /** Popover 纵向布局:控件 w-full,不加 sm 宽度节奏。 */
  fullWidth?: boolean;
}

function FilterControl({ fieldKey, def, value, onChange, fullWidth }: FilterControlProps) {
  const tc = useTranslations("common");
  const tep = useTranslations("entityPicker");
  const tb = useTranslations("billing");
  if (def.kind === "time") {
    return (
      <TimeRangeFilter
        value={value}
        onChange={onChange}
        label={tb("dateRange")}
        maxDays={def.maxHourDays}
        fullWidth={fullWidth}
      />
    );
  }
  if (def.kind === "picker") {
    const label = def.label ?? def.placeholder ?? tep(`label.${def.entity}` as never) ?? fieldKey;
    return (
      <FilterField label={label} className="w-full sm:w-auto">
        <EntityPicker
          key={def.entity}
          entity={def.entity}
          size="sm"
          value={String(value[fieldKey] ?? "")}
          onChange={(v) => onChange({ [fieldKey]: v })}
          placeholder={tc("all")}
          className={cn(
            "w-full [&_button[role=combobox]]:h-9 sm:[&_button[role=combobox]]:h-8",
            !fullWidth && "sm:w-40",
          )}
        />
      </FilterField>
    );
  }
  if (def.kind === "enum") {
    const current = String(value[fieldKey] ?? "");
    const includeAll = def.includeAll !== false;
    const label = def.label ?? def.placeholder ?? fieldKey;
    return (
      <FilterField label={label} className="w-full sm:w-auto">
        <Select
          value={current || "__all__"}
          onValueChange={(v) =>
            onChange({ [fieldKey]: v === "__all__" ? "" : v })
          }
        >
          <SelectTrigger size="sm" className={cn("!h-9 w-full sm:!h-8", !fullWidth && "sm:w-40")}>
            <SelectValue placeholder={def.placeholder} />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {includeAll && (
                <SelectItem value="__all__">{tc("all")}</SelectItem>
              )}
              {def.options.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </FilterField>
    );
  }
  return (
    <FilterField
      label={def.label ?? def.placeholder ?? fieldKey}
      className="w-full sm:w-auto"
    >
      <DebouncedTextFilter
        placeholder={def.placeholder}
        value={String(value[fieldKey] ?? "")}
        debounceMs={def.debounceMs ?? 300}
        onChange={(v) => onChange({ [fieldKey]: v })}
        fullWidth={fullWidth}
      />
    </FilterField>
  );
}

interface TimeRangeFilterProps {
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
  label: string;
  maxDays?: number;
  fullWidth?: boolean;
}

function TimeRangeFilter({ value, onChange, label, maxDays, fullWidth }: TimeRangeFilterProps) {
  const displayRange = buildCompleteDateRange(
    isFiniteUnixSeconds(value.start) ? tsToDateStr(value.start) : "",
    isFiniteUnixSeconds(value.end) ? tsToDateStr(value.end) : "",
    maxDays,
  );

  return (
    <FilterField label={label} className="w-full sm:w-auto">
      <DateRangePicker
        value={displayRange}
        onValueChange={({ startDate, endDate }) => {
          const completeRange = buildCompleteDateRange(startDate, endDate, maxDays);
          // behavior change: update both URL bounds atomically.
          onChange({
            start: dateStrToTs(completeRange.startDate, false),
            end: dateStrToTs(completeRange.endDate, true),
          });
        }}
        placeholder={label}
        maxDays={maxDays}
        size="sm"
        className={cn(
          "[&_[data-slot=date-range-trigger]]:h-9 sm:[&_[data-slot=date-range-trigger]]:h-8",
          fullWidth && "w-full sm:w-full",
        )}
      />
    </FilterField>
  );
}

interface DebouncedTextProps {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  debounceMs: number;
  fullWidth?: boolean;
}

function DebouncedTextFilter({
  value,
  onChange,
  placeholder,
  debounceMs,
  fullWidth,
}: DebouncedTextProps) {
  const [draft, setDraft] = useState(() => ({ baseline: value, value }));
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);
  const currentDraft = useMemo(
    () => draft.baseline === value ? draft : { baseline: value, value },
    [draft, value],
  );
  const debounced = useDebounce(currentDraft, debounceMs);

  useEffect(() => {
    if (debounced.baseline === value && debounced.value !== value) {
      onChangeRef.current(debounced.value);
    }
  }, [debounced, value]);

  return (
    <div className={cn("relative w-full", !fullWidth && "sm:w-56")}>
      <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        placeholder={placeholder}
        value={currentDraft.value}
        onChange={(e) => setDraft({ baseline: value, value: e.target.value })}
        className="h-9 pl-8 sm:h-8"
      />
    </div>
  );
}

function ToolbarActionButton({ action }: { action: ToolbarAction }) {
  const { label, icon, onClick, href, variant = "outline", disabled, loading } =
    action;
  const isDisabled = disabled || loading;
  const inner = (
    <>
      {loading ? <Loader2 data-icon="inline-start" className="animate-spin" /> : icon}
      <span>{label}</span>
    </>
  );
  if (href) {
    return (
      <Button
        asChild
        variant={variant}
        size="sm"
        disabled={isDisabled}
        className="text-body"
      >
        <Link href={href}>{inner}</Link>
      </Button>
    );
  }
  return (
    <Button
      variant={variant}
      size="sm"
      disabled={isDisabled}
      onClick={onClick}
      className="text-body"
    >
      {inner}
    </Button>
  );
}

function ToolbarActionMenuItem({ action }: { action: ToolbarAction }) {
  const { label, icon, onClick, href, disabled, loading, variant } = action;
  const isDisabled = disabled || loading;
  const inner = (
    <>
      {loading ? <Loader2 className="animate-spin" /> : icon}
      <span className={cn(variant === "destructive" && "text-destructive")}>
        {label}
      </span>
    </>
  );
  if (href) {
    return (
      <DropdownMenuItem asChild disabled={isDisabled} className="text-body">
        <Link href={href}>{inner}</Link>
      </DropdownMenuItem>
    );
  }
  return (
    <DropdownMenuItem
      disabled={isDisabled}
      onSelect={onClick}
      className="text-body"
    >
      {inner}
    </DropdownMenuItem>
  );
}

function ToolbarActionsMenu({ actions, label }: { actions: ToolbarAction[]; label: string }) {
  if (actions.length === 0) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="h-9 text-body sm:h-8">
          <MoreHorizontal data-icon="inline-start" />
          <span>{label}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          {actions.map((action, index) => (
            <ToolbarActionMenuItem key={index} action={action} />
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

interface AdvancedFiltersPopoverProps {
  entries: Array<[string, FilterSpec[string]]>;
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
  count: number;
  label: string;
}

function AdvancedFiltersPopover({ entries, value, onChange, count, label }: AdvancedFiltersPopoverProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="relative size-9 overflow-visible px-0 text-body sm:h-8 sm:w-auto sm:px-3"
        >
          <SlidersHorizontal data-icon="inline-start" />
          <span className="sr-only sm:not-sr-only">{label}</span>
          {count > 0 && (
            <Badge
              variant="secondary"
              className="absolute -right-1 -top-1 h-4 min-w-4 rounded-full px-1 text-[10px] leading-none sm:static sm:h-5 sm:min-w-5 sm:px-1.5 sm:text-xs"
            >
              {count}
            </Badge>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="flex w-64 flex-col gap-3">
        {entries.map(([key, def]) => (
          <FilterControl
            key={key}
            fieldKey={key}
            def={def}
            value={value}
            onChange={onChange}
            fullWidth
          />
        ))}
      </PopoverContent>
    </Popover>
  );
}
