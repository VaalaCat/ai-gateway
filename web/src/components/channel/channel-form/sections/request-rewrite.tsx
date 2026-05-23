"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { JsonField } from "@/components/business/json-field";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";

export interface RequestRewriteSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
}

export function RequestRewriteSection({
  form,
  setForm,
  hiddenFields,
}: RequestRewriteSectionProps) {
  const t = useTranslations("channels");

  return (
    <div className="space-y-4">
      {/* Organization */}
      <div className="space-y-2">
        <Label>
          {t("organization")}
          <FieldTip text={t("organizationTip")} />
        </Label>
        <Input
          value={form.organization}
          onChange={(e) => setForm({ ...form, organization: e.target.value })}
          placeholder="org-xxx"
        />
      </div>

      {/* API Version */}
      <div className="space-y-2">
        <Label>
          {t("apiVersion")}
          <FieldTip text={t("apiVersionTip")} />
        </Label>
        <Input
          value={form.api_version}
          onChange={(e) => setForm({ ...form, api_version: e.target.value })}
          placeholder="2024-02-15-preview"
        />
      </div>

      {/* System Prompt */}
      <div className="space-y-2">
        <Label>
          {t("systemPrompt")}
          <FieldTip text={t("systemPromptTip")} />
        </Label>
        <Textarea
          value={form.system_prompt}
          onChange={(e) => setForm({ ...form, system_prompt: e.target.value })}
          rows={3}
        />
      </div>

      {/* Proxy URL */}
      {!hiddenFields?.has("proxy_url") && (
        <div className="space-y-2">
          <Label>
            {t("proxy")}
            <FieldTip text={t("proxyTip")} />
          </Label>
          <Input
            value={form.proxy_url}
            onChange={(e) => setForm({ ...form, proxy_url: e.target.value })}
            placeholder="http://proxy:8080"
          />
        </div>
      )}

      {/* Param Override */}
      <JsonField
        label={t("paramOverride")}
        value={form.param_override}
        onChange={(v) => setForm({ ...form, param_override: v })}
        placeholder='{"temperature": 0.7}'
        tip={<FieldTip text={t("paramOverrideTip")} />}
      />

      {/* Header Override */}
      {!hiddenFields?.has("header_override") && (
        <JsonField
          label={t("headerOverride")}
          value={form.header_override}
          onChange={(v) => setForm({ ...form, header_override: v })}
          placeholder='{"X-Custom": "value"}'
          tip={<FieldTip text={t("headerOverrideTip")} />}
        />
      )}

      {/* Status Code Mapping */}
      <JsonField
        label={t("statusCodeMapping")}
        value={form.status_code_mapping}
        onChange={(v) => setForm({ ...form, status_code_mapping: v })}
        placeholder='{"502": 500}'
        tip={<FieldTip text={t("statusCodeMappingTip")} />}
      />
    </div>
  );
}
