"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FieldTip } from "@/components/business/field-tip";
import type { ChannelTypeMeta } from "@/lib/types";
import { ChannelForm } from "../types";

function formatTypeName(name: string, i18nKey: string, t: ReturnType<typeof useTranslations<"channels">>): string {
  if (i18nKey) { try { return t(i18nKey as never); } catch { /* fallback */ } }
  return name;
}

export interface MetaSectionProps<Entity = unknown> {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  channelTypes: ChannelTypeMeta[];
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
  keyFieldHelpText?: (entity: Entity | null) => string | undefined;
  entity?: Entity | null;
}

export function MetaSection<Entity>({ form, setForm, channelTypes, hiddenFields, keyFieldHelpText, entity = null }: MetaSectionProps<Entity>) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const channelType = Number(form.type);
  const typeOptions = [...channelTypes];
  if (Number.isFinite(channelType) && channelType > 0 && !typeOptions.some((i) => i.id === channelType)) {
    typeOptions.push({ id: channelType, name: "Unknown", i18n_key: "" });
  }
  typeOptions.sort((a, b) => a.id - b.id);

  return (
    <div className="space-y-4">
      {!hiddenFields?.has("use_legacy_adaptor") && (
        <div className={form.use_legacy_adaptor ? "flex items-center justify-between rounded-md border border-yellow-500/30 bg-yellow-500/5 px-4 py-3" : "flex items-center justify-between rounded-md border px-4 py-3"}>
          <div className="space-y-0.5">
            <Label>{t("useLegacyAdaptor")}</Label>
            <p className="text-sm text-muted-foreground">{t("useLegacyAdaptorTip")}</p>
          </div>
          <Switch checked={form.use_legacy_adaptor} onCheckedChange={(v) => setForm({ ...form, use_legacy_adaptor: v })} />
        </div>
      )}
      {form.use_legacy_adaptor && (
        <div className="space-y-2">
          <Label>{t("type")}</Label>
          <Select value={form.type} onValueChange={(v) => setForm({ ...form, type: v })}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              {typeOptions.map((item) => (
                <SelectItem key={item.id} value={String(item.id)}>{`${formatTypeName(item.name, item.i18n_key, t)} [${item.id}]`}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <div className="space-y-2">
        <Label>{tc("name")}</Label>
        <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
      </div>
      <div className="space-y-2">
        <Label>{t("apiKey")}</Label>
        <Input value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} />
        {keyFieldHelpText && (() => { const hint = keyFieldHelpText(entity); return hint ? <p className="text-xs text-muted-foreground mt-1">{hint}</p> : null; })()}
      </div>
      <div className="space-y-2">
        <Label>{t("baseUrl")}</Label>
        <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} />
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>{t("tag")}<FieldTip text={t("tagTip")} /></Label>
          <Input value={form.tag} onChange={(e) => setForm({ ...form, tag: e.target.value })} />
        </div>
        <div className="space-y-2">
          <Label>{t("remark")}<FieldTip text={t("remarkTip")} /></Label>
          <Input value={form.remark} onChange={(e) => setForm({ ...form, remark: e.target.value })} />
        </div>
      </div>
    </div>
  );
}
