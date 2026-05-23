"use client";

import { useTranslations } from "next-intl";
import { ChevronDown } from "lucide-react";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";
import type { ChannelOtherSettings, BuiltinToolFallbackPolicy } from "../types";
import {
  parseOtherSettings,
  stringifyOtherSettings,
  channelProtocols,
} from "../utils";

export interface ProtocolBehaviorSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function ProtocolBehaviorSection({ form, setForm }: ProtocolBehaviorSectionProps) {
  const t = useTranslations("channels");

  const otherSettings = parseOtherSettings(form.other_settings);
  const protos = channelProtocols(form.endpoints);

  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) => {
    setForm({
      ...form,
      other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }),
    });
  };

  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <button
          type="button"
          className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent"
        >
          {t("sectionProtocolBehavior")}
          <ChevronDown className="size-4" />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4 pt-4">
        {/* Claude-only settings */}
        {protos.claude && (
          <>
            <div className="flex items-center justify-between">
              <Label>
                {t("claudeBetaQuery")}
                <FieldTip text={t("claudeBetaQueryTip")} />
              </Label>
              <Switch
                checked={!!otherSettings.claude_beta_query}
                onCheckedChange={(v) => updateOtherSettings({ claude_beta_query: v })}
              />
            </div>
            <div className="flex items-center justify-between">
              <Label>
                {t("allowInferenceGeo")}
                <FieldTip text={t("allowInferenceGeoTip")} />
              </Label>
              <Switch
                checked={!!otherSettings.allow_inference_geo}
                onCheckedChange={(v) => updateOtherSettings({ allow_inference_geo: v })}
              />
            </div>
          </>
        )}

        {/* OpenAI chat or responses settings */}
        {(protos.openaiChat || protos.openaiResponses) && (
          <div className="flex items-center justify-between">
            <Label>
              {t("allowServiceTier")}
              <FieldTip text={t("allowServiceTierTip")} />
            </Label>
            <Switch
              checked={!!otherSettings.allow_service_tier}
              onCheckedChange={(v) => updateOtherSettings({ allow_service_tier: v })}
            />
          </div>
        )}

        {/* Responses-only settings */}
        {protos.openaiResponses && (
          <div className="flex items-center justify-between">
            <Label>
              {t("disableStore")}
              <FieldTip text={t("disableStoreTip")} />
            </Label>
            <Switch
              checked={!!otherSettings.disable_store}
              onCheckedChange={(v) => updateOtherSettings({ disable_store: v })}
            />
          </div>
        )}

        {/* OpenAI chat or responses settings */}
        {(protos.openaiChat || protos.openaiResponses) && (
          <div className="flex items-center justify-between">
            <Label>
              {t("allowIncludeObfuscation")}
              <FieldTip text={t("allowIncludeObfuscationTip")} />
            </Label>
            <Switch
              checked={!!otherSettings.allow_include_obfuscation}
              onCheckedChange={(v) => updateOtherSettings({ allow_include_obfuscation: v })}
            />
          </div>
        )}

        {/* Always shown */}
        <div className="flex items-center justify-between">
          <Label>
            {t("allowSafetyIdentifier")}
            <FieldTip text={t("allowSafetyIdentifierTip")} />
          </Label>
          <Switch
            checked={!!otherSettings.allow_safety_identifier}
            onCheckedChange={(v) => updateOtherSettings({ allow_safety_identifier: v })}
          />
        </div>

        <div className="flex items-center justify-between">
          <Label>
            {t("builtinToolFallback")}
            <FieldTip text={t("builtinToolFallbackTip")} />
          </Label>
          <Select
            value={otherSettings.builtin_tool_fallback || "drop"}
            onValueChange={(v) =>
              updateOtherSettings({ builtin_tool_fallback: v as BuiltinToolFallbackPolicy })
            }
          >
            <SelectTrigger className="w-56">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="drop">{t("builtinToolFallbackDrop")}</SelectItem>
              <SelectItem value="error">{t("builtinToolFallbackError")}</SelectItem>
              <SelectItem value="passthrough">{t("builtinToolFallbackPassthrough")}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="flex items-center justify-between rounded-md border p-3">
          <div className="space-y-0.5">
            <Label htmlFor="system_prompt_in_input">
              {t("fieldSystemPromptInInput")}
            </Label>
            <p className="text-xs text-muted-foreground">
              {t("fieldSystemPromptInInputHint")}
            </p>
          </div>
          <Switch
            id="system_prompt_in_input"
            checked={form.system_prompt_in_input}
            onCheckedChange={(v) =>
              setForm({ ...form, system_prompt_in_input: v })
            }
          />
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
