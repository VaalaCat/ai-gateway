"use client";

import { useEffect, useState, type ReactNode } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { Loader2, MoreHorizontal, Search } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { DateRangeInputs } from "@/components/business/date-range-inputs";
import { useDebounce } from "@/hooks/use-debounce";
import { useAuth } from "@/lib/auth";
import { tsToDateStr, dateStrToTs } from "@/lib/utils/date-range";

import type { FilterSpec, FilterValues, FilterContext } from "./filter-spec";
import type { ToolbarAction } from "./toolbar-actions";

const SECONDARY_COLLAPSE_THRESHOLD = 3;

interface FilterableToolbarProps {
  spec: FilterSpec;
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
  /** 自定义可见性上下文（与 isAdmin 合并）。 */
  context?: Partial<FilterContext>;
  primaryAction?: ReactNode;
  secondaryActions?: ToolbarAction[];
}

export function FilterableToolbar({
  spec,
  value,
  onChange,
  context,
  primaryAction,
  secondaryActions,
}: FilterableToolbarProps) {
  const tc = useTranslations("common");
  const { isAdmin } = useAuth();
  const ctx: FilterContext = { isAdmin, ...context };

  const secondary = secondaryActions ?? [];
  const shouldCollapse = secondary.length >= SECONDARY_COLLAPSE_THRESHOLD;
  const hasActions = primaryAction !== undefined || secondary.length > 0;

  return (
    <div className="flex flex-col gap-3 md:flex-row md:flex-wrap md:items-start md:justify-between md:gap-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-start md:flex-1">
        {Object.entries(spec).map(([key, def]) => {
          if (def.visible && !def.visible(ctx)) return null;
          return (
            <FilterControl
              key={key}
              fieldKey={key}
              def={def}
              value={value}
              onChange={onChange}
            />
          );
        })}
      </div>

      {hasActions && (
        <div className="flex flex-wrap items-center gap-2 md:shrink-0">
          {shouldCollapse ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm" className="text-body">
                  <MoreHorizontal className="size-4" />
                  <span className="ml-1 hidden sm:inline">{tc("more")}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {secondary.map((a, i) => (
                  <ToolbarActionMenuItem key={i} action={a} />
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            secondary.map((a, i) => <ToolbarActionButton key={i} action={a} />)
          )}
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
}

function FilterControl({ fieldKey, def, value, onChange }: FilterControlProps) {
  if (def.kind === "time") {
    return <TimeRangeFilter value={value} onChange={onChange} />;
  }
  if (def.kind === "picker") {
    return (
      <EntityPicker
        entity={def.entity}
        value={String(value[fieldKey] ?? "")}
        onChange={(v) => onChange({ [fieldKey]: v })}
        placeholder={def.placeholder}
        className="w-full sm:w-48"
      />
    );
  }
  if (def.kind === "enum") {
    const current = String(value[fieldKey] ?? "");
    const includeAll = def.includeAll !== false;
    return (
      <Select
        value={current || "__all__"}
        onValueChange={(v) =>
          onChange({ [fieldKey]: v === "__all__" ? "" : v })
        }
      >
        <SelectTrigger className="w-full sm:w-40">
          <SelectValue placeholder={def.placeholder} />
        </SelectTrigger>
        <SelectContent>
          {includeAll && (
            <SelectItem value="__all__">{def.placeholder ?? "全部"}</SelectItem>
          )}
          {def.options.map((opt) => (
            <SelectItem key={opt.value} value={opt.value}>
              {opt.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    );
  }
  return (
    <DebouncedTextFilter
      placeholder={def.placeholder}
      value={String(value[fieldKey] ?? "")}
      debounceMs={def.debounceMs ?? 300}
      onChange={(v) => onChange({ [fieldKey]: v })}
    />
  );
}

interface TimeRangeFilterProps {
  value: FilterValues;
  onChange: (next: Partial<FilterValues>) => void;
}

function TimeRangeFilter({ value, onChange }: TimeRangeFilterProps) {
  const start = Number(value.start ?? 0);
  const end = Number(value.end ?? 0);

  const handleStartChange = (s: string) => {
    onChange({ start: dateStrToTs(s, false) });
  };
  const handleEndChange = (s: string) => {
    onChange({ end: dateStrToTs(s, true) });
  };

  return (
    <DateRangeInputs
      compact
      startDate={tsToDateStr(start)}
      endDate={end > 0 ? tsToDateStr(end - 86400) : ""}
      onStartDateChange={handleStartChange}
      onEndDateChange={handleEndChange}
    />
  );
}

interface DebouncedTextProps {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  debounceMs: number;
}

function DebouncedTextFilter({
  value,
  onChange,
  placeholder,
  debounceMs,
}: DebouncedTextProps) {
  const [local, setLocal] = useState(value);
  const debounced = useDebounce(local, debounceMs);

  useEffect(() => {
    setLocal(value);
  }, [value]);

  useEffect(() => {
    if (debounced !== value) {
      onChange(debounced);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced]);

  return (
    <div className="relative w-full sm:max-w-sm">
      <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        placeholder={placeholder}
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        className="pl-8"
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
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      <span className="ml-2">{label}</span>
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
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      <span
        className={cn("ml-2", variant === "destructive" && "text-destructive")}
      >
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
