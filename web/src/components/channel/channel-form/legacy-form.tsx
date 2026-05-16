"use client";

import { useTranslations } from "next-intl";
import { ChevronDown } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ModelMappingInput } from "@/components/ui/model-mapping-input";
import { ModelSelectorPanel } from "@/components/business/model-selector-panel";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import { StatusSelect } from "@/components/business/status-select";
import { JsonField } from "@/components/business/json-field";
import { FetchModelsButton } from "@/components/business/fetch-models-button";
import { FieldTip } from "@/components/business/field-tip";
import { AgentRouteEditor } from "@/components/agent-route-editor";

import { CHANNEL_TYPES } from "@/lib/constants";
import { ChannelForm } from "@/components/channel/channel-form/types";
import type { ChannelSettings, ChannelOtherSettings } from "@/components/channel/channel-form/types";
import {
  parseSetting,
  stringifySetting,
  parseOtherSettings,
  stringifyOtherSettings,
} from "@/components/channel/channel-form/utils";

export interface LegacyChannelFormProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  channelTypes: { id: number; name: string; i18n_key: string }[];
  showStatus?: boolean;
  agentId?: string;
  channelId?: number;
}

export function LegacyChannelForm({
  form,
  setForm,
  channelTypes,
  showStatus,
  agentId,
  channelId,
}: LegacyChannelFormProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");

  const channelType = Number(form.type);
  const typeOptions = [...channelTypes];
  if (Number.isFinite(channelType) && channelType > 0 && !typeOptions.some((item) => item.id === channelType)) {
    typeOptions.push({ id: channelType, name: "Unknown", i18n_key: "" });
  }
  typeOptions.sort((a, b) => a.id - b.id);
  const setting = parseSetting(form.setting);
  const otherSettings = parseOtherSettings(form.other_settings);

  const updateSetting = (patch: Partial<ChannelSettings>) => {
    setForm({ ...form, setting: stringifySetting({ ...setting, ...patch }) });
  };
  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) => {
    setForm({ ...form, other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }) });
  };

  const formatTypeName = (name: string, i18nKey: string) => {
    if (i18nKey) {
      try {
        return t(i18nKey as never);
      } catch {
        // Fall back to backend-provided canonical name when i18n key is missing.
      }
    }
    return name;
  };

  /* --- Shared: Basic Configuration --- */
  const basicFields = (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label>{tc("name")}</Label>
        <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
      </div>
      {form.use_legacy_adaptor && (
        <div className="space-y-2">
          <Label>{t("type")}</Label>
          <Select value={form.type} onValueChange={(v) => setForm({ ...form, type: v })}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              {typeOptions.map((item) => (
                <SelectItem key={item.id} value={String(item.id)}>
                  {`${formatTypeName(item.name, item.i18n_key)} [${item.id}]`}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <div className="space-y-2">
        <Label>{t("apiKey")}</Label>
        <Input value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} />
      </div>
      <div className="space-y-2">
        <Label>{t("baseUrl")}</Label>
        <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} />
      </div>
      {showStatus && (
        <StatusSelect value={form.status} onChange={(v) => setForm({ ...form, status: v })} />
      )}
    </div>
  );

  /* --- Shared: Model Configuration --- */
  const modelFields = (
    <div className="space-y-4">
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
            existingModels={form.models ? form.models.split(",").map(s => s.trim()).filter(Boolean) : []}
            onModelsSelected={(models) => setForm({ ...form, models: models.join(",") })}
          />
        </div>
        <ModelSelectorPanel
          value={form.models ? form.models.split(",").map(s => s.trim()).filter(Boolean) : []}
          onChange={(models) => setForm({ ...form, models: models.join(",") })}
        />
      </div>
      <div className="space-y-2">
        <Label>{t("modelMapping")}</Label>
        <ModelMappingInput
          value={form.model_mapping}
          onChange={(json) => setForm({ ...form, model_mapping: json })}
          onMappingAdd={(sourceModel) => {
            const modelList = form.models ? form.models.split(",").map(s => s.trim()).filter(Boolean) : [];
            if (!modelList.includes(sourceModel)) {
              setForm({ ...form, models: [...modelList, sourceModel].join(",") });
            }
          }}
          onMappingRemove={(sourceModel) => {
            const modelList = form.models.split(",").map(s => s.trim()).filter(m => m && m !== sourceModel);
            setForm({ ...form, models: modelList.join(",") });
          }}
        />
      </div>
    </div>
  );

  /* --- Shared: Load Balancing --- */
  const loadBalancingFields = (
    <Collapsible defaultOpen>
      <CollapsibleTrigger asChild>
        <button type="button" className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent">
          {t("sectionLoadBalancing")}
          <ChevronDown className="size-4" />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4 pt-4">
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>{t("weight")}</Label>
            <Input type="number" value={form.weight} onChange={(e) => setForm({ ...form, weight: e.target.value })} />
          </div>
          <div className="space-y-2">
            <Label>{t("priority")}</Label>
            <Input type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })} />
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );

  /* --- Shared: Provider-specific (conditional) --- */
  const providerFields = (channelType === CHANNEL_TYPES.OPENAI || channelType === CHANNEL_TYPES.AZURE || channelType === CHANNEL_TYPES.VERTEX_AI) ? (
    <div className="space-y-4 rounded-md border px-4 py-3">
      <p className="text-sm font-medium">{t("sectionProvider")}</p>
      {channelType === CHANNEL_TYPES.OPENAI && (
        <div className="space-y-2">
          <Label>{t("organization")}<FieldTip text={t("organizationTip")} /></Label>
          <Input value={form.organization} onChange={(e) => setForm({ ...form, organization: e.target.value })} placeholder="org-xxx" />
        </div>
      )}
      {(channelType === CHANNEL_TYPES.AZURE || channelType === CHANNEL_TYPES.VERTEX_AI) && (
        <div className="space-y-2">
          <Label>{t("apiVersion")}<FieldTip text={t("apiVersionTip")} /></Label>
          <Input value={form.api_version} onChange={(e) => setForm({ ...form, api_version: e.target.value })} placeholder="2024-02-15-preview" />
        </div>
      )}
    </div>
  ) : null;

  /* ================================================================
   * LEGACY MODE
   * ================================================================ */
  return (
    <div className="space-y-4 py-4">
      {/* Legacy Mode Toggle */}
      <div className="flex items-center justify-between rounded-md border border-yellow-500/30 bg-yellow-500/5 px-4 py-3">
        <div className="space-y-0.5">
          <Label>{t("useLegacyAdaptor")}</Label>
          <p className="text-sm text-muted-foreground">{t("useLegacyAdaptorTip")}</p>
        </div>
        <Switch
          checked={form.use_legacy_adaptor}
          onCheckedChange={(v) => setForm({ ...form, use_legacy_adaptor: v })}
        />
      </div>

      {/* Group 1: Basic */}
      {basicFields}

      {/* Group 2: Models */}
      {modelFields}

      {/* Group 3: Load Balancing */}
      {loadBalancingFields}

      {/* Group 4: Provider-specific (conditional) */}
      {providerFields}

      {/* Group 5: Relay Behavior */}
      <Collapsible defaultOpen>
        <CollapsibleTrigger asChild>
          <button type="button" className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent">
            {t("sectionRelay")}
            <ChevronDown className="size-4" />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-4 pt-4">
          <div className="flex items-center justify-between">
            <Label>{t("forceFormat")}<FieldTip text={t("forceFormatTip")} /></Label>
            <Switch checked={!!setting.force_format} onCheckedChange={(v) => updateSetting({ force_format: v })} />
          </div>
          <div className="flex items-center justify-between">
            <Label>{t("thinkingToContent")}<FieldTip text={t("thinkingToContentTip")} /></Label>
            <Switch checked={!!setting.thinking_to_content} onCheckedChange={(v) => updateSetting({ thinking_to_content: v })} />
          </div>
          <div className="space-y-2">
            <Label>{t("proxy")}<FieldTip text={t("proxyTip")} /></Label>
            <Input value={setting.proxy || ""} onChange={(e) => updateSetting({ proxy: e.target.value })} placeholder="http://proxy:8080" />
          </div>
          <div className="space-y-2">
            <Label>{t("systemPrompt")}<FieldTip text={t("systemPromptTip")} /></Label>
            <Textarea value={setting.system_prompt || ""} onChange={(e) => updateSetting({ system_prompt: e.target.value })} rows={3} />
          </div>
          <div className="flex items-center justify-between">
            <Label>{t("systemPromptOverride")}<FieldTip text={t("systemPromptOverrideTip")} /></Label>
            <Switch checked={!!setting.system_prompt_override} onCheckedChange={(v) => updateSetting({ system_prompt_override: v })} />
          </div>
          <div className="flex items-center justify-between">
            <Label>{t("passThroughBody")}<FieldTip text={t("passThroughBodyTip")} /></Label>
            <Switch checked={!!setting.pass_through_body_enabled} onCheckedChange={(v) => updateSetting({ pass_through_body_enabled: v })} />
          </div>
        </CollapsibleContent>
      </Collapsible>

      {/* Group 6: Request Customization (collapsed) */}
      <Collapsible>
        <CollapsibleTrigger asChild>
          <button type="button" className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent">
            {t("sectionOverride")}
            <ChevronDown className="size-4" />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-4 pt-4">
          <JsonField
            label={t("paramOverride")}
            value={form.param_override}
            onChange={(v) => setForm({ ...form, param_override: v })}
            placeholder='{"temperature": 0.7}'
            tip={<FieldTip text={t("paramOverrideTip")} />}
          />
          <JsonField
            label={t("headerOverride")}
            value={form.header_override}
            onChange={(v) => setForm({ ...form, header_override: v })}
            placeholder='{"X-Custom": "value"}'
            tip={<FieldTip text={t("headerOverrideTip")} />}
          />
          <JsonField
            label={t("statusCodeMapping")}
            value={form.status_code_mapping}
            onChange={(v) => setForm({ ...form, status_code_mapping: v })}
            placeholder='{"502": 500}'
            tip={<FieldTip text={t("statusCodeMappingTip")} />}
          />
        </CollapsibleContent>
      </Collapsible>

      {/* Group 7: Advanced Switches (collapsed) */}
      <Collapsible>
        <CollapsibleTrigger asChild>
          <button type="button" className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent">
            {t("sectionAdvanced")}
            <ChevronDown className="size-4" />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-4 pt-4">
          <div className="rounded-md border border-yellow-500/30 bg-yellow-500/5 px-4 py-2 text-xs text-yellow-600 dark:text-yellow-400">
            {t("advancedWarning")}
          </div>
          <div className="flex items-center justify-between">
            <Label>{t("autoBan")}<FieldTip text={t("autoBanTip")} /></Label>
            <Switch checked={form.auto_ban === "1"} onCheckedChange={(v) => setForm({ ...form, auto_ban: v ? "1" : "0" })} />
          </div>
          {channelType === CHANNEL_TYPES.ANTHROPIC && (
            <>
              <div className="flex items-center justify-between">
                <Label>{t("claudeBetaQuery")}<FieldTip text={t("claudeBetaQueryTip")} /></Label>
                <Switch checked={!!otherSettings.claude_beta_query} onCheckedChange={(v) => updateOtherSettings({ claude_beta_query: v })} />
              </div>
              <div className="flex items-center justify-between">
                <Label>{t("allowInferenceGeo")}<FieldTip text={t("allowInferenceGeoTip")} /></Label>
                <Switch checked={!!otherSettings.allow_inference_geo} onCheckedChange={(v) => updateOtherSettings({ allow_inference_geo: v })} />
              </div>
            </>
          )}
          {channelType === CHANNEL_TYPES.OPENAI && (
            <>
              <div className="flex items-center justify-between">
                <Label>{t("allowServiceTier")}<FieldTip text={t("allowServiceTierTip")} /></Label>
                <Switch checked={!!otherSettings.allow_service_tier} onCheckedChange={(v) => updateOtherSettings({ allow_service_tier: v })} />
              </div>
              <div className="flex items-center justify-between">
                <Label>{t("disableStore")}<FieldTip text={t("disableStoreTip")} /></Label>
                <Switch checked={!!otherSettings.disable_store} onCheckedChange={(v) => updateOtherSettings({ disable_store: v })} />
              </div>
              <div className="flex items-center justify-between">
                <Label>{t("allowIncludeObfuscation")}<FieldTip text={t("allowIncludeObfuscationTip")} /></Label>
                <Switch checked={!!otherSettings.allow_include_obfuscation} onCheckedChange={(v) => updateOtherSettings({ allow_include_obfuscation: v })} />
              </div>
            </>
          )}
          <div className="flex items-center justify-between">
            <Label>{t("allowSafetyIdentifier")}<FieldTip text={t("allowSafetyIdentifierTip")} /></Label>
            <Switch checked={!!otherSettings.allow_safety_identifier} onCheckedChange={(v) => updateOtherSettings({ allow_safety_identifier: v })} />
          </div>
          {channelType === CHANNEL_TYPES.AZURE && (
            <div className="space-y-2">
              <Label>{t("azureResponsesVersion")}<FieldTip text={t("azureResponsesVersionTip")} /></Label>
              <Input value={otherSettings.azure_responses_version || ""} onChange={(e) => updateOtherSettings({ azure_responses_version: e.target.value })} />
            </div>
          )}
          {channelType === CHANNEL_TYPES.VERTEX_AI && (
            <div className="space-y-2">
              <Label>{t("vertexKeyType")}<FieldTip text={t("vertexKeyTypeTip")} /></Label>
              <Select value={otherSettings.vertex_key_type || "json"} onValueChange={(v) => updateOtherSettings({ vertex_key_type: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="json">JSON (Service Account)</SelectItem>
                  <SelectItem value="api_key">API Key</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          {channelType === CHANNEL_TYPES.AWS && (
            <div className="space-y-2">
              <Label>{t("awsKeyType")}<FieldTip text={t("awsKeyTypeTip")} /></Label>
              <Select value={otherSettings.aws_key_type || "ak_sk"} onValueChange={(v) => updateOtherSettings({ aws_key_type: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="ak_sk">AK/SK</SelectItem>
                  <SelectItem value="api_key">API Key</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="space-y-2">
            <Label>{t("testModel")}<FieldTip text={t("testModelTip")} /></Label>
            <Input value={form.test_model} onChange={(e) => setForm({ ...form, test_model: e.target.value })} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{t("tag")}<FieldTip text={t("tagTip")} /></Label>
              <Input value={form.tag} onChange={(e) => setForm({ ...form, tag: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label>{t("remark")}<FieldTip text={t("remarkTip")} /></Label>
              <Input value={form.remark} onChange={(e) => setForm({ ...form, remark: e.target.value })} />
            </div>
          </div>
        </CollapsibleContent>
      </Collapsible>
      {channelId !== undefined && (
        <AgentRouteEditor sourceType="channel" sourceId={channelId} />
      )}
    </div>
  );
}
