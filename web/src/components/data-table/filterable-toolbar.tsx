"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
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
import { DatePicker, parseDateStr } from "@/components/business/date-picker/date-picker";
import { useDebounce } from "@/hooks/use-debounce";
import { useAuth } from "@/lib/auth";
import { tsToDateStr, dateStrToTs } from "@/lib/utils/date-range";

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
  const hasActions = primaryAction !== undefined || secondary.length > 0 || secondaryContent !== undefined;

  const entries = Object.entries(spec).filter(
    ([, def]) => !def.visible || def.visible(ctx),
  );
  const primary = entries.filter(([, def]) => !def.advanced);
  const advanced = entries.filter(([, def]) => Boolean(def.advanced));
  const activeAdvancedCount = advanced.filter(([key]) =>
    isActiveFilterValue(value[key]),
  ).length;

  return (
    <div className="flex flex-col gap-3 md:flex-row md:flex-wrap md:items-end md:justify-between md:gap-4">
      {entries.length > 0 && (
        <div
          data-slot="toolbar-filters"
          className={cn(
            "flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-end",
            filtersOnOwnRow ? "sm:gap-2 md:basis-full" : "sm:gap-3 md:flex-1",
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
          {advanced.length > 0 && (
            <AdvancedFiltersPopover
              entries={advanced}
              value={value}
              onChange={onChange}
              count={activeAdvancedCount}
              label={tc("filters")}
            />
          )}
        </div>
      )}

      {hasActions && (
        <div
          data-slot="toolbar-actions"
          className={cn(
            "flex flex-wrap items-center gap-2 md:shrink-0",
            (filtersOnOwnRow || entries.length === 0) && "md:ml-auto",
          )}
        >
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
        labels={{ start: tb("startDate"), end: tb("endDate") }}
        fullWidth={fullWidth}
      />
    );
  }
  if (def.kind === "picker") {
    const label = def.label ?? def.placeholder ?? tep(`label.${def.entity}` as never) ?? fieldKey;
    return (
      <FilterField label={label} className={fullWidth ? "w-full" : undefined}>
        <EntityPicker
          entity={def.entity}
          size="sm"
          value={String(value[fieldKey] ?? "")}
          onChange={(v) => onChange({ [fieldKey]: v })}
          placeholder={tc("all")}
          className={cn("w-full", !fullWidth && "sm:w-40")}
        />
      </FilterField>
    );
  }
  if (def.kind === "enum") {
    const current = String(value[fieldKey] ?? "");
    const includeAll = def.includeAll !== false;
    const label = def.label ?? def.placeholder ?? fieldKey;
    return (
      <FilterField label={label} className={fullWidth ? "w-full" : undefined}>
        <Select
          value={current || "__all__"}
          onValueChange={(v) =>
            onChange({ [fieldKey]: v === "__all__" ? "" : v })
          }
        >
          <SelectTrigger size="sm" className={cn("w-full", !fullWidth && "sm:w-40")}>
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
      className={fullWidth ? "w-full" : undefined}
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
  labels: { start: string; end: string };
  fullWidth?: boolean;
}

function TimeRangeFilter({ value, onChange, labels, fullWidth }: TimeRangeFilterProps) {
  // 复用 date-range-inputs.tsx 非紧凑档惯例:Label 已经表达字段含义,
  // DatePicker placeholder 用通用「选择日期」,避免与 FilterField label 重复文案。
  const tc = useTranslations("common");
  const start = Number(value.start ?? 0);
  const end = Number(value.end ?? 0);
  const startStr = tsToDateStr(start);
  const endStr = end > 0 ? tsToDateStr(end - 86400) : "";
  const startParsed = parseDateStr(startStr);
  const endParsed = parseDateStr(endStr);

  return (
    <>
      <FilterField label={labels.start} className={fullWidth ? "w-full" : undefined}>
        <DatePicker
          size="sm"
          value={startStr}
          onChange={(s) => onChange({ start: dateStrToTs(s, false) })}
          placeholder={tc("selectDate")}
          disabledRange={endParsed ? { after: endParsed } : undefined}
          className={cn(
            fullWidth && "w-full sm:[&_[data-slot=popover-trigger]]:w-full",
          )}
        />
      </FilterField>
      <FilterField label={labels.end} className={fullWidth ? "w-full" : undefined}>
        <DatePicker
          size="sm"
          value={endStr}
          onChange={(s) => onChange({ end: dateStrToTs(s, true) })}
          placeholder={tc("selectDate")}
          disabledRange={startParsed ? { before: startParsed } : undefined}
          className={cn(
            fullWidth && "w-full sm:[&_[data-slot=popover-trigger]]:w-full",
          )}
        />
      </FilterField>
    </>
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
  const currentDraft = useMemo(
    () => draft.baseline === value ? draft : { baseline: value, value },
    [draft, value],
  );
  const debounced = useDebounce(currentDraft, debounceMs);

  useEffect(() => {
    if (debounced.baseline === value && debounced.value !== value) {
      onChange(debounced.value);
    }
  }, [debounced, onChange, value]);

  return (
    <div className={cn("relative w-full", !fullWidth && "sm:w-56")}>
      <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        placeholder={placeholder}
        value={currentDraft.value}
        onChange={(e) => setDraft({ baseline: value, value: e.target.value })}
        className="h-8 pl-8"
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
        <Button variant="outline" size="sm" className="text-body">
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
        <Button variant="outline" size="sm" className="text-body">
          <SlidersHorizontal data-icon="inline-start" />
          <span>{label}</span>
          {count > 0 && (
            <Badge variant="secondary" className="h-5 min-w-5 rounded-full px-1.5 text-xs">
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
