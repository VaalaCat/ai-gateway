"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ChannelForm } from "../types";
import { parseResilience, stringifyResilience, ResilienceOverride } from "../utils";

const FIELDS: Array<{ key: keyof ResilienceOverride; labelKey: string }> = [
  { key: "max_retries",         labelKey: "maxRetries" },
  { key: "backoff_base_ms",     labelKey: "backoffBaseMs" },
  { key: "backoff_max_ms",      labelKey: "backoffMaxMs" },
  { key: "breaker_threshold",   labelKey: "breakerThreshold" },
  { key: "breaker_cooldown_ms", labelKey: "breakerCooldownMs" },
];

export interface ResilienceSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function ResilienceSection({ form, setForm }: ResilienceSectionProps) {
  const t = useTranslations("channels");
  const resilience = parseResilience(form.resilience);
  const updateField = (key: keyof ResilienceOverride, raw: string) => {
    const next: ResilienceOverride = { ...resilience };
    if (raw === "") delete next[key];
    else { const n = Number(raw); if (!Number.isNaN(n)) next[key] = n; }
    setForm({ ...form, resilience: stringifyResilience(next) });
  };
  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">{t("resilienceHint")}</p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
