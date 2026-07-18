"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";
import type { ChannelOtherSettings } from "../types";
import {
  channelProtocols,
  parseOtherSettings,
  stringifyOtherSettings,
} from "../utils";

export interface ConnectionSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
}

export function ConnectionSection({ form, setForm, hiddenFields }: ConnectionSectionProps) {
  const t = useTranslations("channels");
  const otherSettings = parseOtherSettings(form.other_settings);
  const protocols = channelProtocols(form.endpoints);
  const openAI = protocols.openaiChat || protocols.openaiResponses;
  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) => setForm({
    ...form,
    other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }),
  });

  return (
    <div className="flex flex-col gap-8">
      <FieldSet>
        <FieldLegend>{t("upstreamProviderParameters")}</FieldLegend>
        <FieldDescription>{t("upstreamProviderParametersDesc")}</FieldDescription>
        <FieldGroup className="gap-4">
          <Field>
            <FieldLabel htmlFor="organization">
              {t("organization")}<FieldTip text={t("organizationTip")} />
            </FieldLabel>
            <Input
              id="organization"
              value={form.organization}
              onChange={(event) => setForm({ ...form, organization: event.target.value })}
              placeholder="org-xxx"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="api_version">
              {t("apiVersion")}<FieldTip text={t("apiVersionTip")} />
            </FieldLabel>
            <Input
              id="api_version"
              value={form.api_version}
              onChange={(event) => setForm({ ...form, api_version: event.target.value })}
              placeholder="2024-02-15-preview"
            />
          </Field>
          {protocols.claude && (
            <PolicySwitch
              id="claude_beta_query"
              label={t("claudeBetaQuery")}
              description={t("claudeBetaQueryTip")}
              checked={!!otherSettings.claude_beta_query}
              onCheckedChange={(checked) => updateOtherSettings({ claude_beta_query: checked })}
            />
          )}
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>{t("requestFieldPolicy")}</FieldLegend>
        <FieldDescription>{t("requestFieldPolicyDesc")}</FieldDescription>
        <FieldGroup className="gap-4">
          {(openAI || protocols.claude) && (
            <PolicySwitch
              id="allow_service_tier"
              label={t("allowServiceTier")}
              description={t("allowServiceTierTip")}
              checked={!!otherSettings.allow_service_tier}
              onCheckedChange={(checked) => updateOtherSettings({ allow_service_tier: checked })}
            />
          )}
          {protocols.claude && (
            <PolicySwitch
              id="allow_inference_geo"
              label={t("allowInferenceGeo")}
              description={t("allowInferenceGeoTip")}
              checked={!!otherSettings.allow_inference_geo}
              onCheckedChange={(checked) => updateOtherSettings({ allow_inference_geo: checked })}
            />
          )}
          {openAI && (
            <PolicySwitch
              id="disable_store"
              label={t("disableStore")}
              description={t("disableStoreTip")}
              checked={!!otherSettings.disable_store}
              onCheckedChange={(checked) => updateOtherSettings({ disable_store: checked })}
            />
          )}
          {openAI && (
            <PolicySwitch
              id="allow_safety_identifier"
              label={t("allowSafetyIdentifier")}
              description={t("allowSafetyIdentifierTip")}
              checked={!!otherSettings.allow_safety_identifier}
              onCheckedChange={(checked) => updateOtherSettings({ allow_safety_identifier: checked })}
            />
          )}
          {protocols.openaiResponses && (
            <PolicySwitch
              id="allow_include_obfuscation"
              label={t("allowIncludeObfuscation")}
              description={t("allowIncludeObfuscationTip")}
              checked={!!otherSettings.allow_include_obfuscation}
              onCheckedChange={(checked) => updateOtherSettings({ allow_include_obfuscation: checked })}
            />
          )}
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>{t("networkConnection")}</FieldLegend>
        <FieldDescription>{t("networkConnectionDesc")}</FieldDescription>
        <FieldGroup className="gap-4">
          {!hiddenFields?.has("proxy_url") && (
            <Field>
              <FieldLabel htmlFor="proxy_url">
                {t("proxy")}<FieldTip text={t("proxyTip")} />
              </FieldLabel>
              <Input
                id="proxy_url"
                value={form.proxy_url}
                onChange={(event) => setForm({ ...form, proxy_url: event.target.value })}
                placeholder="http://proxy:8080"
              />
            </Field>
          )}
          {!hiddenFields?.has("disable_keepalive") && (
            <PolicySwitch
              id="disable_keepalive"
              label={t("disableKeepalive")}
              description={t("disableKeepaliveHint")}
              checked={form.disable_keepalive}
              onCheckedChange={(checked) => setForm({ ...form, disable_keepalive: checked })}
            />
          )}
        </FieldGroup>
      </FieldSet>
    </div>
  );
}

function PolicySwitch({
  id,
  label,
  description,
  checked,
  onCheckedChange,
}: {
  id: string;
  label: string;
  description: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <Field orientation="horizontal">
      <FieldContent>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <FieldDescription>{description}</FieldDescription>
      </FieldContent>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
    </Field>
  );
}
