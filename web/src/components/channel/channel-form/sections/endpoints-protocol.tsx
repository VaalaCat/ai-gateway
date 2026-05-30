"use client";

import { useTranslations } from "next-intl";
import { ChevronDown } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ProtocolRuleCard } from "../protocol-rule-card";
import { ThinkingPassthroughCard } from "../thinking-passthrough-card";
import { ChannelForm, ENDPOINT_DEFAULTS, ENDPOINT_OPTIONS } from "../types";
import type { ChannelOtherSettings } from "../types";
import {
  parseEndpoints,
  stringifyEndpoints,
  parseOtherSettings,
  stringifyOtherSettings,
  channelProtocols,
} from "../utils";

export interface EndpointsProtocolSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function EndpointsProtocolSection({ form, setForm }: EndpointsProtocolSectionProps) {
  const t = useTranslations("channels");

  const eps = parseEndpoints(form.endpoints);
  const baseUrl = form.base_url ? form.base_url.replace(/\/+$/, "") : "";
  const otherSettings = parseOtherSettings(form.other_settings);

  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) => {
    setForm({
      ...form,
      other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }),
    });
  };

  const toggleEndpoint = (key: string, checked: boolean) => {
    const updated = { ...eps };
    if (checked) {
      (updated as Record<string, string>)[key] = ENDPOINT_DEFAULTS[key];
    } else {
      delete (updated as Record<string, string | undefined>)[key];
    }
    setForm({ ...form, endpoints: stringifyEndpoints(updated) });
  };

  const updatePath = (key: string, path: string) => {
    const updated = { ...eps, [key]: path };
    setForm({ ...form, endpoints: stringifyEndpoints(updated) });
  };

  const protos = channelProtocols(form.endpoints);
  const enabledCount =
    (protos.openaiChat ? 1 : 0) + (protos.openaiResponses ? 1 : 0) + (protos.claude ? 1 : 0);

  const enabledOutbounds: Array<{
    value: "openai_chat" | "openai_responses" | "claude";
    label: string;
  }> = [];
  if (protos.openaiChat) enabledOutbounds.push({ value: "openai_chat", label: "openai_chat" });
  if (protos.openaiResponses)
    enabledOutbounds.push({ value: "openai_responses", label: "openai_responses" });
  if (protos.claude) enabledOutbounds.push({ value: "claude", label: "claude" });

  const overrideMap = otherSettings.protocol_override || {};

  const renderProtocolOverrideRow = (inbound: "openai_chat" | "openai_responses" | "claude") => {
    const value = overrideMap[inbound] || "auto";
    return (
      <div className="flex items-center justify-between" key={inbound}>
        <Label>{inbound}</Label>
        <Select
          value={value}
          onValueChange={(v) => {
            const next = { ...overrideMap };
            if (v === "auto" || v === inbound) {
              delete next[inbound]; // 清理 auto / identity 不入库
            } else {
              next[inbound] = v as "openai_chat" | "openai_responses" | "claude";
            }
            updateOtherSettings({
              protocol_override: Object.keys(next).length === 0 ? undefined : next,
            });
          }}
        >
          <SelectTrigger className="w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">{t("protocolOverrideAuto")}</SelectItem>
            {enabledOutbounds.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  };

  return (
    <div className="space-y-4">
      {/* Endpoint toggles + passthrough */}
      <div className="space-y-4 rounded-md border px-4 py-3">
        <p className="text-sm font-medium">{t("sectionEndpoints")}</p>

        <div className="space-y-3">
          {ENDPOINT_OPTIONS.map((opt) => {
            const enabled = opt.key in eps;
            const path = eps[opt.key] || "";
            const fullUrl = baseUrl && path ? `${baseUrl}${path}` : "";
            return (
              <div key={opt.key} className="space-y-2">
                <label className="flex items-center gap-2 text-sm cursor-pointer">
                  <Checkbox
                    checked={enabled}
                    onCheckedChange={(v) => toggleEndpoint(opt.key, !!v)}
                  />
                  <span className="font-medium">{t(opt.labelKey)}</span>
                </label>
                {enabled && (
                  <div className="ml-6 space-y-1">
                    <div className="flex items-center gap-2">
                      <Label className="text-xs w-10 shrink-0">{t("endpointPath")}</Label>
                      <Input
                        value={path}
                        onChange={(e) => updatePath(opt.key, e.target.value)}
                        placeholder={opt.default}
                        className="h-8 text-xs font-mono"
                      />
                    </div>
                    {fullUrl && (
                      <p className="text-xs text-muted-foreground font-mono ml-12 break-all">
                        {"→"} {fullUrl}
                      </p>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>

        <div className="flex items-center justify-between pt-2 border-t">
          <div className="space-y-0.5">
            <Label>{t("passthroughEnabled")}</Label>
            <p className="text-sm text-muted-foreground">{t("passthroughEnabledTip")}</p>
          </div>
          <Switch
            checked={form.passthrough_enabled}
            onCheckedChange={(v) => setForm({ ...form, passthrough_enabled: v })}
          />
        </div>
      </div>

      {/* Protocol Override (gated by ≥2 enabled endpoints) */}
      {enabledCount >= 2 && (
        <Collapsible>
          <CollapsibleTrigger asChild>
            <button
              type="button"
              className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent"
            >
              {t("protocolOverride")}
              <ChevronDown className="size-4" />
            </button>
          </CollapsibleTrigger>
          <CollapsibleContent className="space-y-4 pt-4">
            <p className="text-sm text-muted-foreground">{t("protocolOverrideTip")}</p>
            {renderProtocolOverrideRow("openai_chat")}
            {renderProtocolOverrideRow("openai_responses")}
            {renderProtocolOverrideRow("claude")}

            {/* Per-Model Override sub-block */}
            <div className="space-y-3 pt-4 border-t">
              <div>
                <Label className="text-sm font-medium">{t("protocolOverridePerModel")}</Label>
                <p className="text-xs text-muted-foreground mt-1">
                  {t("protocolOverridePerModelTip")}
                </p>
              </div>

              {(otherSettings.model_protocol_override ?? []).map((rule, idx) => (
                <ProtocolRuleCard
                  key={idx}
                  rule={rule}
                  enabledOutbounds={enabledOutbounds}
                  onChange={(next) => {
                    const list = [...(otherSettings.model_protocol_override ?? [])];
                    list[idx] = next;
                    updateOtherSettings({
                      model_protocol_override: list.length === 0 ? undefined : list,
                    });
                  }}
                  onDelete={() => {
                    const list = (otherSettings.model_protocol_override ?? []).filter(
                      (_, i) => i !== idx
                    );
                    updateOtherSettings({
                      model_protocol_override: list.length === 0 ? undefined : list,
                    });
                  }}
                />
              ))}

              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  const list = [...(otherSettings.model_protocol_override ?? [])];
                  list.push({ model: "", overrides: {} });
                  updateOtherSettings({ model_protocol_override: list });
                }}
              >
                + {t("protocolOverridePerModelAddRule")}
              </Button>
            </div>

            {/* ── thinking_passthrough 区块（追加） ── */}
            <div className="space-y-3 pt-4 border-t">
              <div>
                <Label className="text-sm font-medium">
                  {t("thinkingPassthrough")}
                </Label>
                <p className="text-xs text-muted-foreground mt-1">
                  {t("thinkingPassthroughTip")}
                </p>
              </div>

              {!protos.openaiChat && (
                <div className="rounded-md border border-yellow-500/40 bg-yellow-500/10 px-3 py-2 text-xs text-yellow-700 dark:text-yellow-200">
                  {t("thinkingPassthroughOnlyOpenAIChat")}
                </div>
              )}

              {(otherSettings.model_thinking_passthrough ?? []).map((rule, idx) => (
                <ThinkingPassthroughCard
                  key={idx}
                  rule={rule}
                  onChange={(next) => {
                    const list = [...(otherSettings.model_thinking_passthrough ?? [])];
                    list[idx] = next;
                    updateOtherSettings({ model_thinking_passthrough: list });
                  }}
                  onDelete={() => {
                    const list = (otherSettings.model_thinking_passthrough ?? []).filter(
                      (_, i) => i !== idx
                    );
                    updateOtherSettings({
                      model_thinking_passthrough: list.length === 0 ? undefined : list,
                    });
                  }}
                />
              ))}

              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  const list = [...(otherSettings.model_thinking_passthrough ?? [])];
                  list.push({ model_pattern: "", send_back_thinking: false });
                  updateOtherSettings({ model_thinking_passthrough: list });
                }}
              >
                + {t("thinkingPassthroughAddRule")}
              </Button>
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
      {enabledCount < 2 && (
        <p className="text-xs text-muted-foreground">{t("protocolOverrideLocked")}</p>
      )}
    </div>
  );
}
