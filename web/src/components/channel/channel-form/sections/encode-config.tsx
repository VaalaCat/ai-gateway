"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { FieldTip } from "@/components/business/field-tip";
import { ProtocolRuleCard } from "../protocol-rule-card";
import { ChannelForm, ENDPOINT_DEFAULTS, ENDPOINT_OPTIONS } from "../types";
import type { ChannelOtherSettings, BuiltinToolFallbackPolicy } from "../types";
import {
  parseEndpoints,
  stringifyEndpoints,
  parseOtherSettings,
  stringifyOtherSettings,
  channelProtocols,
} from "../utils";

export interface EncodeConfigSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function EncodeConfigSection({ form, setForm }: EncodeConfigSectionProps) {
  const t = useTranslations("channels");

  // ── shared helpers ──
  const otherSettings = parseOtherSettings(form.other_settings);
  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) =>
    setForm({ ...form, other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }) });

  // ── endpoints helpers ──
  const eps = parseEndpoints(form.endpoints);
  const baseUrl = form.base_url ? form.base_url.replace(/\/+$/, "") : "";

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

  // ── protocol-override helpers ──
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
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between" key={inbound}>
        <Label>{inbound}</Label>
        <Select
          value={value}
          onValueChange={(v) => {
            const next = { ...overrideMap };
            if (v === "auto" || v === inbound) {
              delete next[inbound];
            } else {
              next[inbound] = v as "openai_chat" | "openai_responses" | "claude";
            }
            updateOtherSettings({
              protocol_override: Object.keys(next).length === 0 ? undefined : next,
            });
          }}
        >
          <SelectTrigger className="w-full sm:w-56">
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

  const endpointsSummary = t("encodeSummaryEndpoints", {
    count: Object.keys(eps).length,
    passthrough: form.passthrough_enabled ? "on" : "off",
  });
  const overrideTotal =
    Object.keys(overrideMap).length + (otherSettings.model_protocol_override?.length ?? 0);
  const overrideSummary =
    enabledCount < 2 ? t("encodeSummaryLocked") : t("encodeSummaryOverrides", { count: overrideTotal });
  const behaviorOnCount = [
    otherSettings.builtin_tool_fallback && otherSettings.builtin_tool_fallback !== "drop",
    form.system_prompt_in_input,
  ].filter(Boolean).length;
  const behaviorSummary = t("encodeSummaryBehavior", { count: behaviorOnCount });

  return (
    <Accordion type="multiple" defaultValue={["endpoints"]} className="rounded-md border">
      {/* ── Group 1: Endpoints ── */}
      <AccordionItem value="endpoints">
        <AccordionTrigger className="px-3">
          <div className="flex flex-1 items-center justify-between pr-2">
            <span>{t("sectionEndpoints")}</span>
            <span className="text-xs font-normal text-muted-foreground">{endpointsSummary}</span>
          </div>
        </AccordionTrigger>
        <AccordionContent className="space-y-3 px-3 pb-3">
          {Object.keys(eps).length === 0 && (
            <p className="text-xs text-destructive">{t("encodeEndpointsRequired")}</p>
          )}
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
        </AccordionContent>
      </AccordionItem>

      {/* ── Group 2: Protocol Override ── */}
      <AccordionItem value="protocol-override">
        <AccordionTrigger className="px-3">
          <div className="flex flex-1 items-center justify-between pr-2">
            <span>{t("protocolOverride")}</span>
            <span className="text-xs font-normal text-muted-foreground">{overrideSummary}</span>
          </div>
        </AccordionTrigger>
        <AccordionContent className="space-y-3 px-3 pb-3">
          {enabledCount >= 2 ? (
            <>
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
            </>
          ) : (
            <p className="text-xs text-muted-foreground">{t("protocolOverrideLocked")}</p>
          )}
        </AccordionContent>
      </AccordionItem>

      {/* ── Group 3: Protocol Behavior ── */}
      <AccordionItem value="protocol-behavior">
        <AccordionTrigger className="px-3">
          <div className="flex flex-1 items-center justify-between pr-2">
            <span>{t("sectionProtocolBehavior")}</span>
            <span className="text-xs font-normal text-muted-foreground">{behaviorSummary}</span>
          </div>
        </AccordionTrigger>
        <AccordionContent className="space-y-3 px-3 pb-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
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
              <SelectTrigger className="w-full sm:w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="drop">{t("builtinToolFallbackDrop")}</SelectItem>
                <SelectItem value="error">{t("builtinToolFallbackError")}</SelectItem>
                <SelectItem value="passthrough">{t("builtinToolFallbackPassthrough")}</SelectItem>
                <SelectItem value="function">{t("builtinToolFallbackFunction")}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center justify-between rounded-md border p-3">
            <div className="space-y-0.5">
              <Label htmlFor="system_prompt_in_input">{t("fieldSystemPromptInInput")}</Label>
              <p className="text-xs text-muted-foreground">{t("fieldSystemPromptInInputHint")}</p>
            </div>
            <Switch
              id="system_prompt_in_input"
              checked={form.system_prompt_in_input}
              onCheckedChange={(v) => setForm({ ...form, system_prompt_in_input: v })}
            />
          </div>
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}
