"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ChannelForm } from "../types";
import { parseResilience, stringifyResilience, ResilienceOverride } from "../utils";

type NumericResilienceKey =
  | "max_retries"
  | "backoff_base_ms"
  | "backoff_max_ms"
  | "breaker_threshold"
  | "breaker_cooldown_ms";

type BreakerOverrideValue = "inherit" | "enabled" | "disabled";

const FIELDS: Array<{ key: NumericResilienceKey; labelKey: string }> = [
  { key: "max_retries", labelKey: "maxRetries" },
  { key: "backoff_base_ms", labelKey: "backoffBaseMs" },
  { key: "backoff_max_ms", labelKey: "backoffMaxMs" },
  { key: "breaker_threshold", labelKey: "breakerThreshold" },
  { key: "breaker_cooldown_ms", labelKey: "breakerCooldownMs" },
];

function breakerOverrideValue(value: boolean | undefined): BreakerOverrideValue {
  if (value === true) return "enabled";
  if (value === false) return "disabled";
  return "inherit";
}

export interface ResilienceSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function ResilienceSection({ form, setForm }: ResilienceSectionProps) {
  const t = useTranslations("channels");
  const resilience = parseResilience(form.resilience);
  const updateField = (key: NumericResilienceKey, raw: string) => {
    const next: ResilienceOverride = { ...resilience };
    if (raw === "") delete next[key];
    else { const n = Number(raw); if (!Number.isNaN(n)) next[key] = n; }
    setForm({ ...form, resilience: stringifyResilience(next) });
  };
  const updateBreakerEnabled = (value: BreakerOverrideValue) => {
    const next: ResilienceOverride = { ...resilience };
    if (value === "inherit") delete next.breaker_enabled;
    else next.breaker_enabled = value === "enabled";
    setForm({ ...form, resilience: stringifyResilience(next) });
  };
  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">{t("resilienceHint")}</p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>{t("breakerEnabledOverride")}</Label>
          <Select
            value={breakerOverrideValue(resilience.breaker_enabled)}
            onValueChange={(value) => updateBreakerEnabled(value as BreakerOverrideValue)}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="inherit">{t("inheritGlobal")}</SelectItem>
              <SelectItem value="enabled">{t("enabled")}</SelectItem>
              <SelectItem value="disabled">{t("disabled")}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {FIELDS.map(({ key, labelKey }) => (
          <div key={key} className="space-y-2">
            <Label>{t(labelKey as never)}</Label>
            <Input type="number" min={0} value={resilience[key] !== undefined ? String(resilience[key]) : ""} onChange={(e) => updateField(key, e.target.value)} />
          </div>
        ))}
      </div>
    </div>
  );
}
