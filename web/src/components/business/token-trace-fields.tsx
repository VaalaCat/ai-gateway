"use client";

import { useId } from "react";
import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { TokenTraceMode } from "@/lib/types";

interface TokenTraceFieldsProps {
  enabled: boolean;
  mode: TokenTraceMode;
  onEnabledChange: (enabled: boolean) => void;
  onModeChange: (mode: TokenTraceMode) => void;
}

export function TokenTraceFields({
  enabled,
  mode,
  onEnabledChange,
  onModeChange,
}: TokenTraceFieldsProps) {
  const t = useTranslations("tokens");
  const traceEnabledId = useId();

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <Label htmlFor={traceEnabledId}>{t("traceEnabled")}</Label>
        <Switch
          id={traceEnabledId}
          checked={enabled}
          onCheckedChange={onEnabledChange}
        />
      </div>
      {enabled ? (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Label>{t("traceContent")}</Label>
          <ToggleGroup
            type="single"
            variant="outline"
            value={mode}
            aria-label={t("traceContent")}
            onValueChange={(value) => {
              if (value === "full" || value === "headers") {
                onModeChange(value);
              }
            }}
          >
            <ToggleGroupItem value="full">{t("traceModeFull")}</ToggleGroupItem>
            <ToggleGroupItem value="headers">{t("traceModeHeaders")}</ToggleGroupItem>
          </ToggleGroup>
        </div>
      ) : null}
    </div>
  );
}

export function TokenTraceBadge({
  enabled,
  mode,
}: {
  enabled: boolean;
  mode?: TokenTraceMode;
}) {
  const t = useTranslations("tokens");
  const label = !enabled
    ? t("traceDisabled")
    : mode === "headers"
      ? t("traceModeHeaders")
      : t("traceModeFull");

  return <Badge variant={enabled ? "secondary" : "outline"}>{label}</Badge>;
}
