"use client";

import { useTranslations } from "next-intl";
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
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { FieldTip } from "@/components/business/field-tip";
import { ProtocolRuleCard } from "../protocol-rule-card";
import { ChannelEndpointsEditor } from "../channel-endpoints-editor";
import { ChannelForm } from "../types";
import type { ChannelOtherSettings, BuiltinToolFallbackPolicy } from "../types";
import {
  parseEndpoints,
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
  const endpoints = parseEndpoints(form.endpoints);
  const updateOtherSettings = (patch: Partial<ChannelOtherSettings>) =>
    setForm({ ...form, other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }) });

  // ── protocol-override helpers ──
  const protos = channelProtocols(form.endpoints);
  const enabledCount =
    (protos.openaiChat ? 1 : 0) + (protos.openaiResponses ? 1 : 0) + (protos.claude ? 1 : 0) + (protos.gemini ? 1 : 0);

  const enabledOutbounds: Array<{
    value: "openai_chat" | "openai_responses" | "claude" | "gemini";
    label: string;
  }> = [];
  if (protos.openaiChat) enabledOutbounds.push({ value: "openai_chat", label: "openai_chat" });
  if (protos.openaiResponses)
    enabledOutbounds.push({ value: "openai_responses", label: "openai_responses" });
  if (protos.claude) enabledOutbounds.push({ value: "claude", label: "claude" });
  if (protos.gemini) enabledOutbounds.push({ value: "gemini", label: "Gemini" });

  const generationRules = otherSettings.model_gemini_generation ?? [];
  const setGenerationRules = (rules: typeof generationRules) =>
    updateOtherSettings({ model_gemini_generation: rules.length ? rules : undefined });

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
              next[inbound] = v as "openai_chat" | "openai_responses" | "claude" | "gemini";
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
    count: Object.keys(endpoints).length,
    passthrough: form.passthrough_enabled ? "on" : "off",
  });
  const overrideTotal =
    Object.keys(overrideMap).length + (otherSettings.model_protocol_override?.length ?? 0);
  const overrideSummary =
    enabledCount < 2 ? t("encodeSummaryLocked") : t("encodeSummaryOverrides", { count: overrideTotal });
  const behaviorOnCount = [
    otherSettings.builtin_tool_fallback && otherSettings.builtin_tool_fallback !== "drop",
    form.system_prompt_in_input,
    generationRules.length > 0,
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
          <ChannelEndpointsEditor
            endpoints={form.endpoints}
            baseURL={form.base_url}
            onEndpointsChange={(endpoints) => setForm({ ...form, endpoints })}
          />

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
          <section className="space-y-3 border-t pt-3">
            <h3 className="text-sm font-medium">{t("geminiGeneration")}</h3>
            <p className="text-xs text-muted-foreground">{t("geminiGenerationTip")}</p>
          {generationRules.map((rule, index) => {
            const update = (patch: Partial<typeof rule>) => setGenerationRules(
              generationRules.map((current, i) => i === index ? { ...current, ...patch } : current)
            );
            const move = (to: number) => {
              const next = [...generationRules];
              [next[index], next[to]] = [next[to], next[index]];
              setGenerationRules(next);
            };
            return (
              <div key={index} className="space-y-3 rounded-md border p-3">
                <Label>{t("toolCompatibilityModelLabel")}
                  <Input value={rule.model_pattern} onChange={e => update({ model_pattern: e.target.value })} />
                </Label>
                <Label>{t("thinkingMode")}
                  <select className="rounded-md border bg-background px-2 py-1" value={rule.mode} onChange={e => update({ mode: e.target.value as typeof rule.mode })}>
                    <option value="level">{t("thinkingLevel")}</option>
                    <option value="budget">{t("thinkingBudget")}</option>
                  </select>
                </Label>
                {(rule.mappings ?? []).map((pair, i, pairs) => {
                  const edit = (patch: Partial<typeof pair>) => update({ mappings: pairs.map((p, j) => i === j ? { ...p, ...patch } : p) });
                  return <div key={i} className="flex items-end gap-2">
                    <Label className="flex-1 flex-col items-start">{t("effortInput")}<Input value={pair.from} onChange={e => edit({ from: e.target.value })} /></Label>
                    <Label className="flex-1 flex-col items-start">{rule.mode === "budget" ? t("thinkingBudget") : t("thinkingLevel")}<Input value={pair.to} onChange={e => edit({ to: e.target.value })} /></Label>
                    <Button type="button" size="sm" variant="ghost" aria-label={t("removeThinkingMapping")} onClick={() => update({ mappings: pairs.filter((_, j) => i !== j) })}>×</Button>
                  </div>;
                })}
                <Button type="button" size="sm" variant="outline" onClick={() => update({ mappings: [...(rule.mappings ?? []), { from: "", to: "" }] })}>+ {t("addThinkingMapping")}</Button>
                <div className="flex gap-2">
                  <Button type="button" size="sm" variant="outline" disabled={index === 0} onClick={() => move(index - 1)}>{t("geminiMoveUp")}</Button>
                  <Button type="button" size="sm" variant="outline" disabled={index === generationRules.length - 1} onClick={() => move(index + 1)}>{t("geminiMoveDown")}</Button>
                  <Button type="button" size="sm" variant="ghost" onClick={() => setGenerationRules(generationRules.filter((_, i) => i !== index))}>{t("delete")}</Button>
                </div>
              </div>
            );
          })}
          <Button type="button" size="sm" variant="outline" onClick={() => setGenerationRules([...generationRules, { model_pattern: "", mode: "level", mappings: [] }])}>
            + {t("geminiAddRule")}
          </Button>
          </section>
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}
