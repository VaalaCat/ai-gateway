"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Badge } from "@/components/ui/badge";
import { useRoutingCandidates, useRoutingCandidatesByToken } from "@/lib/api/model-routings";
import { cn } from "@/lib/utils";

export interface RefComboboxProps {
  value: string;
  onChange: (v: string) => void;
  alreadyAdded?: string[];
  excludeSelf?: string;
  hideRoutings?: boolean;
  apiMode?: "admin" | "user";
  tokenKey?: string | null;
  limitCandidatesToToken?: boolean;
}

export function RefCombobox({
  value,
  onChange,
  alreadyAdded = [],
  excludeSelf,
  hideRoutings = false,
  apiMode = "admin",
  tokenKey,
  limitCandidatesToToken = false,
}: RefComboboxProps) {
  const t = useTranslations("modelRoutings.members");
  const [open, setOpen] = useState(false);
  // 两个 hook 必须无条件调用——admin 模式下 useRoutingCandidates 启用，
  // user 模式下 useRoutingCandidatesByToken 启用，反之则各自 disabled。
  const useTokenCandidates = apiMode === "user" || limitCandidatesToToken;
  const adminQuery = useRoutingCandidates({ enabled: !useTokenCandidates });
  const userQuery = useRoutingCandidatesByToken(
    useTokenCandidates ? tokenKey ?? null : null,
  );
  const data = useTokenCandidates ? userQuery.data : adminQuery.data;

  const routings = (data?.global_routings ?? []).filter((n) => n !== excludeSelf);
  // 非自身路由名不混进 Models 组：同名时 off-path 解析为路由（见后端规则）。
  // excludeSelf 已从 routings 排除，故编辑路由 X 时同名真实模型 X 仍保留在 Models 组。
  const models = (data?.models ?? []).filter((n) => !routings.includes(n));

  const isAlreadyAdded = (n: string) => alreadyAdded.includes(n) && n !== value;
  const kindOf = (n: string) => (routings.includes(n) ? "routing" : "model");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          tabIndex={-1}
          className="w-full justify-between text-body"
        >
          {value ? (
            <span className="flex items-center gap-2 truncate">
              <span className="truncate">{value}</span>
              <Badge variant="outline" className="text-xs shrink-0">
                {kindOf(value) === "routing" ? t("refKindRouting") : t("refKindModel")}
              </Badge>
            </span>
          ) : (
            <span className="text-muted-foreground">{t("refPlaceholder")}</span>
          )}
          <ChevronsUpDown className="size-4 opacity-50 shrink-0" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[--radix-popover-trigger-width] p-0"
        align="start"
      >
        <Command>
          <CommandInput placeholder={t("refSearch")} />
          <CommandList>
            <CommandEmpty>{t("refNoMatches")}</CommandEmpty>
            {models.length === 0 && routings.length === 0 && (
              <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                {t("emptyCandidates")}
              </div>
            )}
            {models.length > 0 && (
              <CommandGroup heading={t("refKindModel")}>
                {models.map((n) => (
                  <CommandItem
                    key={`m-${n}`}
                    value={n}
                    disabled={isAlreadyAdded(n)}
                    onSelect={() => {
                      onChange(n);
                      setOpen(false);
                    }}
                  >
                    <Check
                      className={cn(
                        "size-4 mr-2",
                        n === value ? "opacity-100" : "opacity-0"
                      )}
                    />
                    <span className="flex-1 truncate">{n}</span>
                    {n === excludeSelf && (
                      <Badge variant="outline" className="ml-2 text-xs">
                        {t("refUnderlyingModel")}
                      </Badge>
                    )}
                    {isAlreadyAdded(n) && (
                      <span className="ml-2 text-xs text-muted-foreground">
                        {t("refAlreadyAdded")}
                      </span>
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {!hideRoutings && routings.length > 0 && (
              <CommandGroup heading={t("refKindRouting")}>
                {routings.map((n) => (
                  <CommandItem
                    key={`r-${n}`}
                    value={n}
                    disabled={isAlreadyAdded(n)}
                    onSelect={() => {
                      onChange(n);
                      setOpen(false);
                    }}
                  >
                    <Check
                      className={cn(
                        "size-4 mr-2",
                        n === value ? "opacity-100" : "opacity-0"
                      )}
                    />
                    <span className="flex-1 truncate">{n}</span>
                    <Badge variant="outline" className="ml-2 text-xs">
                      {t("refKindRoutingShort")}
                    </Badge>
                    {isAlreadyAdded(n) && (
                      <span className="ml-2 text-xs text-muted-foreground">
                        {t("refAlreadyAdded")}
                      </span>
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
