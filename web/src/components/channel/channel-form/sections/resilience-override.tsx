"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronDown } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type { ChannelForm } from "../types";
import { parseResilience, stringifyResilience, ResilienceOverride } from "../utils";

export interface ResilienceOverrideSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

type ResilienceKey = keyof ResilienceOverride;

const FIELDS: Array<{ key: ResilienceKey; labelKey: string }> = [
  { key: "max_retries",        labelKey: "maxRetries" },
  { key: "backoff_base_ms",    labelKey: "backoffBaseMs" },
  { key: "backoff_max_ms",     labelKey: "backoffMaxMs" },
  { key: "breaker_threshold",  labelKey: "breakerThreshold" },
  { key: "breaker_cooldown_ms", labelKey: "breakerCooldownMs" },
];

export function ResilienceOverrideSection({ form, setForm }: ResilienceOverrideSectionProps) {
  const t = useTranslations("channels");
  const [open, setOpen] = useState(false);

  const resilience = parseResilience(form.resilience);

  const updateField = (key: ResilienceKey, raw: string) => {
    const next: ResilienceOverride = { ...resilience };
    if (raw === "") {
      delete next[key];
    } else {
      const n = Number(raw);
      if (!Number.isNaN(n)) {
        next[key] = n;
      }
    }
    setForm({ ...form, resilience: stringifyResilience(next) });
  };

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <button
          type="button"
          className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent"
        >
          {t("resilienceOverride")}
          <ChevronDown className="size-4" />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4 pt-4">
        <p className="text-xs text-muted-foreground">{t("resilienceHint")}</p>
        <div className="grid grid-cols-2 gap-4">
          {FIELDS.map(({ key, labelKey }) => (
            <div key={key} className="space-y-2">
              <Label>
                {t(labelKey as never)}
              </Label>
              <Input
                type="number"
                min={0}
                value={resilience[key] !== undefined ? String(resilience[key]) : ""}
                onChange={(e) => updateField(key, e.target.value)}
              />
            </div>
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
