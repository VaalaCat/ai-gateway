"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { JsonField } from "@/components/business/json-field";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";

export interface ResponseSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
}

export function ResponseSection({ form, setForm, hiddenFields }: ResponseSectionProps) {
  const t = useTranslations("channels");
  return (
    <div className="space-y-4">
      <JsonField label={t("statusCodeMapping")} value={form.status_code_mapping} onChange={(v) => setForm({ ...form, status_code_mapping: v })} placeholder='{"502": 500}' tip={<FieldTip text={t("statusCodeMappingTip")} />} />
      {!hiddenFields?.has("free") && (
        <div className="flex items-center justify-between">
          <Label>{t("free")}<FieldTip text={t("freeTip")} /></Label>
          <Switch checked={form.free} onCheckedChange={(v) => setForm({ ...form, free: v })} />
        </div>
      )}
      {!hiddenFields?.has("price_ratio") && (
        <div className="space-y-2">
          <Label>{t("priceRatio")}<FieldTip text={t("priceRatioTip")} /></Label>
          <Input type="number" step="0.01" min="0" max="1000" value={form.price_ratio} disabled={form.free} onChange={(e) => setForm({ ...form, price_ratio: e.target.value })} />
        </div>
      )}
    </div>
  );
}
