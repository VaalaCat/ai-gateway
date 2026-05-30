"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronDown, Plus, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { DateTimePicker } from "@/components/business/date-picker/date-time-picker";
import { UNIT_QUOTA_SCALE } from "@/lib/utils/format";
import type { ChannelForm } from "../types";
import { parseLimit, stringifyLimit, ChannelLimit } from "../utils";

export interface UsageLimitSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

type Rule = NonNullable<ChannelLimit["rules"]>[number];

const METRICS: Array<Rule["metric"]> = ["calls", "cost"];
const WINDOWS: Array<Rule["window"]> = ["lifetime", "daily", "weekly", "monthly", "rolling_days"];

export function UsageLimitSection({ form, setForm }: UsageLimitSectionProps) {
  const t = useTranslations("channels");
  const [open, setOpen] = useState(false);

  const limit = parseLimit(form.limit);
  const rules: Rule[] = limit.rules ?? [];

  const commit = (next: ChannelLimit) => setForm({ ...form, limit: stringifyLimit(next) });

  const updateRule = (idx: number, patch: Partial<Rule>) => {
    const next = rules.map((r, i) => (i === idx ? { ...r, ...patch } : r));
    commit({ ...limit, rules: next });
  };
  const addRule = () => {
    const blank: Rule = { metric: "cost", window: "monthly", threshold: 0 };
    commit({ ...limit, rules: [...rules, blank] });
  };
  const removeRule = (idx: number) => {
    commit({ ...limit, rules: rules.filter((_, i) => i !== idx) });
  };

  const thresholdDisplay = (r: Rule): string => {
    if (r.metric === "cost") return r.threshold ? String(r.threshold / UNIT_QUOTA_SCALE) : "";
    return r.threshold ? String(r.threshold) : "";
  };
  const onThresholdChange = (idx: number, r: Rule, raw: string) => {
    const n = Number(raw);
    const v = Number.isNaN(n) ? 0 : n;
    const threshold = r.metric === "cost" ? Math.round(v * UNIT_QUOTA_SCALE) : Math.round(v);
    updateRule(idx, { threshold });
  };

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <button
          type="button"
          className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent"
        >
          {t("usageLimit")}
          <ChevronDown className="size-4" />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4 pt-4">
        <p className="text-xs text-muted-foreground">{t("usageLimitHint")}</p>

        <div className="space-y-2">
          <Label>{t("limitCutoff")}</Label>
          <DateTimePicker
            value={limit.disable_at || null}
            onChange={(v) => commit({ ...limit, disable_at: v ?? 0 })}
            placeholder={t("limitCutoff")}
          />
          <p className="text-xs text-muted-foreground">{t("limitCutoffHint")}</p>
        </div>

        <div className="space-y-2">
          <Label>{t("limitRules")}</Label>
          {rules.map((r, idx) => (
            <div key={idx} className="flex flex-wrap items-end gap-2 rounded-md border p-2">
              <div className="space-y-1">
                <Label className="text-xs">{t("limitMetric")}</Label>
                <Select value={r.metric} onValueChange={(v) => updateRule(idx, { metric: v as Rule["metric"] })}>
                  <SelectTrigger className="w-28"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {METRICS.map((m) => (
                      <SelectItem key={m} value={m}>{t(m === "calls" ? "metricCalls" : "metricCost")}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label className="text-xs">{t("limitWindow")}</Label>
                <Select value={r.window} onValueChange={(v) => updateRule(idx, { window: v as Rule["window"] })}>
                  <SelectTrigger className="w-28"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {WINDOWS.map((w) => (
                      <SelectItem key={w} value={w}>
                        {t(
                          w === "lifetime" ? "windowLifetime"
                          : w === "daily" ? "windowDaily"
                          : w === "weekly" ? "windowWeekly"
                          : w === "monthly" ? "windowMonthly"
                          : "windowRollingDays"
                        )}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label className="text-xs">{t("limitThreshold")}</Label>
                <Input
                  type="number"
                  min={0}
                  className="w-32"
                  value={thresholdDisplay(r)}
                  onChange={(e) => onThresholdChange(idx, r, e.target.value)}
                />
              </div>
              {r.window === "rolling_days" && (
                <div className="space-y-1">
                  <Label className="text-xs">{t("limitDays")}</Label>
                  <Input
                    type="number"
                    min={1}
                    className="w-20"
                    value={r.days ?? ""}
                    onChange={(e) => updateRule(idx, { days: Math.max(1, Number(e.target.value) || 1) })}
                  />
                </div>
              )}
              <Button type="button" variant="ghost" size="icon" onClick={() => removeRule(idx)} aria-label={t("limitDelete")}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          ))}
          <Button type="button" variant="outline" size="sm" onClick={addRule}>
            <Plus className="size-4" /> {t("limitAddRule")}
          </Button>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
