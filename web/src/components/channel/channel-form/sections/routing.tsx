"use client";

import { useTranslations } from "next-intl";
import { Plus, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { StatusSelect } from "@/components/business/status-select";
import { ModelSelectorPanel } from "@/components/business/model-selector-panel";
import { FetchModelsButton } from "@/components/business/fetch-models-button";
import { CatalogPickerDialog } from "@/components/business/catalog-picker-dialog";
import { FieldTip } from "@/components/business/field-tip";
import { AgentRouteEditor } from "@/components/agent-route-editor";
import { DateTimePicker } from "@/components/business/date-picker/date-time-picker";
import { UNIT_QUOTA_SCALE } from "@/lib/utils/format";
import { ChannelForm } from "../types";
import { parseSetting, parseLimit, stringifyLimit, ChannelLimit } from "../utils";

export interface RoutingSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
  useModelsCatalog?: () => { data: string[] | undefined };
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
  showStatus?: boolean;
  channelId?: number;
}

type Rule = NonNullable<ChannelLimit["rules"]>[number];

const METRICS: Array<Rule["metric"]> = ["calls", "cost"];
const WINDOWS: Array<Rule["window"]> = ["lifetime", "daily", "weekly", "monthly", "rolling_days"];

function splitModels(models: string): string[] {
  return models ? models.split(",").map((s) => s.trim()).filter(Boolean) : [];
}

export function RoutingSection({
  form,
  setForm,
  agentId,
  useModelsCatalog,
  hiddenFields,
  showStatus,
  channelId,
}: RoutingSectionProps) {
  const t = useTranslations("channels");

  // Usage limit state
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
    <div className="space-y-4">
      {/* 1. Status (edit mode only) */}
      {showStatus && (
        <StatusSelect
          value={form.status}
          onChange={(v) => setForm({ ...form, status: v })}
        />
      )}

      {/* 2. Models list — variant-aware */}
      {useModelsCatalog ? (
        <CatalogModelsListBlock form={form} setForm={setForm} useModelsCatalog={useModelsCatalog} />
      ) : (
        <AdminModelsListBlock form={form} setForm={setForm} agentId={agentId} />
      )}

      {/* 3. Weight + Priority (de-collapsed) */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>{t("weight")}</Label>
          <Input
            type="number"
            min={1}
            value={form.weight}
            onChange={(e) => setForm({ ...form, weight: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label>{t("priority")}</Label>
          <Input
            type="number"
            value={form.priority}
            onChange={(e) => setForm({ ...form, priority: e.target.value })}
          />
        </div>
      </div>

      {/* 4. Test Model */}
      <div className="space-y-2">
        <Label>
          {t("testModel")}
          <FieldTip text={t("testModelTip")} />
        </Label>
        <Input
          value={form.test_model}
          onChange={(e) => setForm({ ...form, test_model: e.target.value })}
        />
      </div>

      {/* 5. Auto Ban */}
      <div className="flex items-center justify-between">
        <Label>
          {t("autoBan")}
          <FieldTip text={t("autoBanTip")} />
        </Label>
        <Switch
          checked={form.auto_ban === "1"}
          onCheckedChange={(v) => setForm({ ...form, auto_ban: v ? "1" : "0" })}
        />
      </div>

      {/* 6. Usage Limit (de-collapsed, inline) */}
      {!hiddenFields?.has("limit") && (
        <div className="space-y-4">
          <Label>{t("usageLimit")}</Label>

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
        </div>
      )}

      {/* 7. Agent Route Editor */}
      {channelId !== undefined ? (
        <AgentRouteEditor sourceType="channel" sourceId={channelId} />
      ) : (
        <p className="text-sm text-muted-foreground">{t("agentRouteCreateHint")}</p>
      )}
    </div>
  );
}

/* ── Catalog variant models block ─────────────────────────────────────── */

interface CatalogModelsListBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  useModelsCatalog: () => { data: string[] | undefined };
}

function CatalogModelsListBlock({ form, setForm, useModelsCatalog }: CatalogModelsListBlockProps) {
  const t = useTranslations("channels");
  const { data } = useModelsCatalog();
  const available = data ?? [];
  const selected = splitModels(form.models);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{t("models")}</Label>
        <CatalogPickerDialog
          available={available}
          alreadySelected={selected}
          disabled={available.length === 0}
          onConfirm={(added) =>
            setForm({
              ...form,
              models: [...selected, ...added].join(","),
            })
          }
        />
      </div>
      <ModelSelectorPanel
        value={selected}
        onChange={(models) => setForm({ ...form, models: models.join(",") })}
      />
    </div>
  );
}

/* ── Admin fetch variant models block ─────────────────────────────────── */

interface AdminModelsListBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
}

function AdminModelsListBlock({ form, setForm, agentId }: AdminModelsListBlockProps) {
  const t = useTranslations("channels");
  const setting = parseSetting(form.setting);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{t("models")}</Label>
        <FetchModelsButton
          baseUrl={form.base_url}
          apiKey={form.key}
          channelType={Number(form.type)}
          endpoints={form.endpoints}
          proxyUrl={form.proxy_url || setting.proxy}
          agentId={agentId}
          existingModels={splitModels(form.models)}
          onModelsSelected={(models) => setForm({ ...form, models: models.join(",") })}
        />
      </div>
      <ModelSelectorPanel
        value={splitModels(form.models)}
        onChange={(models) => setForm({ ...form, models: models.join(",") })}
      />
    </div>
  );
}
