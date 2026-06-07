"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";

export interface ConnectionSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
}

export function ConnectionSection({ form, setForm, hiddenFields }: ConnectionSectionProps) {
  const t = useTranslations("channels");
  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label>{t("organization")}<FieldTip text={t("organizationTip")} /></Label>
        <Input value={form.organization} onChange={(e) => setForm({ ...form, organization: e.target.value })} placeholder="org-xxx" />
      </div>
      <div className="space-y-2">
        <Label>{t("apiVersion")}<FieldTip text={t("apiVersionTip")} /></Label>
        <Input value={form.api_version} onChange={(e) => setForm({ ...form, api_version: e.target.value })} placeholder="2024-02-15-preview" />
      </div>
      {!hiddenFields?.has("proxy_url") && (
        <div className="space-y-2">
          <Label>{t("proxy")}<FieldTip text={t("proxyTip")} /></Label>
          <Input value={form.proxy_url} onChange={(e) => setForm({ ...form, proxy_url: e.target.value })} placeholder="http://proxy:8080" />
        </div>
      )}
      {!hiddenFields?.has("disable_keepalive") && (
        <div className="flex items-center justify-between rounded-md border p-3">
          <div className="space-y-0.5">
            <Label htmlFor="disable_keepalive">{t("disableKeepalive")}</Label>
            <p className="text-xs text-muted-foreground">{t("disableKeepaliveHint")}</p>
          </div>
          <Switch id="disable_keepalive" checked={form.disable_keepalive} onCheckedChange={(v) => setForm({ ...form, disable_keepalive: v })} />
        </div>
      )}
    </div>
  );
}
