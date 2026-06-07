"use client";

import { useTranslations } from "next-intl";
import { Textarea } from "@/components/ui/textarea";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { JsonField } from "@/components/business/json-field";
import { FieldTip } from "@/components/business/field-tip";
import { ModelMappingInput } from "@/components/ui/model-mapping-input";
import { RoleMappingEditor } from "@/components/channel/role-mapping-editor";
import { ThinkingPassthroughCard } from "../thinking-passthrough-card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ChevronDown } from "lucide-react";
import { useChannelDataFlow } from "@/lib/api/channels";
import type { ChannelDataFlowStep } from "@/lib/types";
import { cn } from "@/lib/utils";
import { ChannelForm } from "../types";
import { parseOtherSettings, stringifyOtherSettings, channelProtocols } from "../utils";
import { EncodeConfigSection } from "./encode-config";

const STEP_ORDER = [
  "model_mapping", "inject_system_prompt", "role_mapping",
  "thinking_passthrough", "thinking_strip",
  "encode", "forward_client_headers", "param_override", "header_override", "upstream_script",
] as const;

// 纯信息工序:展开只会露出一句说明 / 一个链接 —— 折叠它们=无用折叠,故留成普通行。
const INLINE_STEPS = new Set<string>(["thinking_strip", "upstream_script", "forward_client_headers"]);

export interface ProcessingSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  channelId?: number;
  scriptsHref?: string;
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
}

export function ProcessingSection({ form, setForm, channelId, scriptsHref, hiddenFields }: ProcessingSectionProps) {
  const t = useTranslations("channels");
  const { data } = useChannelDataFlow(channelId ?? 0, { enabled: channelId !== undefined });

  const steps: ChannelDataFlowStep[] = data?.steps ?? STEP_ORDER.map((key): ChannelDataFlowStep => ({ key, title: "", config_ref: "", active: true }));

  const isStepHidden = (key: string): boolean => {
    if (key === "header_override") return !!hiddenFields?.has("header_override");
    if (key === "upstream_script") return !scriptsHref;
    return false;
  };
  const visibleSteps = steps.filter((s) => !isStepHidden(s.key));

  const splitModels = (m: string) => (m ? m.split(",").map((s) => s.trim()).filter(Boolean) : []);

  const renderStepConfig = (key: string) => {
    switch (key) {
      case "model_mapping":
        return (
          <ModelMappingInput
            value={form.model_mapping}
            onChange={(json) => setForm({ ...form, model_mapping: json })}
            onMappingAdd={(src) => { const l = splitModels(form.models); if (!l.includes(src)) setForm({ ...form, models: [...l, src].join(",") }); }}
            onMappingRemove={(src) => setForm({ ...form, models: splitModels(form.models).filter((m) => m !== src).join(",") })}
          />
        );
      case "inject_system_prompt":
        return (
          <div className="space-y-2">
            <Label>{t("systemPrompt")}<FieldTip text={t("systemPromptTip")} /></Label>
            <Textarea value={form.system_prompt} onChange={(e) => setForm({ ...form, system_prompt: e.target.value })} rows={3} />
          </div>
        );
      case "role_mapping":
        return <RoleMappingEditor value={form.role_mapping} onChange={(v) => setForm({ ...form, role_mapping: v })} />;
      case "thinking_passthrough":
        return <ThinkingRulesEditor form={form} setForm={setForm} />;
      case "thinking_strip":
        return <p className="text-xs text-muted-foreground">{t("thinkingStripFollowsRules")}</p>;
      case "forward_client_headers":
        return <p className="text-xs text-muted-foreground">{t("forwardClientHeadersInfo")}</p>;
      case "encode":
        return <EncodeConfigSection form={form} setForm={setForm} />;
      case "param_override":
        return <JsonField label={t("paramOverride")} value={form.param_override} onChange={(v) => setForm({ ...form, param_override: v })} placeholder='{"temperature": 0.7}' tip={<FieldTip text={t("paramOverrideTip")} />} />;
      case "header_override":
        return <JsonField label={t("headerOverride")} value={form.header_override} onChange={(v) => setForm({ ...form, header_override: v })} placeholder='{"X-Custom": "value"}' tip={<FieldTip text={t("headerOverrideTip")} />} />;
      case "upstream_script":
        return <a href={scriptsHref} className="text-sm text-primary underline">{t("upstreamScriptConfigureLink")}</a>;
      default:
        return null;
    }
  };

  return (
    <div className="space-y-1">
      {data?.resolved_protocol && (
        <p className="text-xs text-muted-foreground pb-2">{t("dataflowResolvedProtocol", { protocol: data.resolved_protocol })}</p>
      )}
      {visibleSteps.map((s) => {
        const isEncode = s.key === "encode";
        const dot = (
          <span className={cn("inline-block size-2 shrink-0 rounded-full border", s.active ? "border-primary bg-primary" : "border-muted-foreground/40")} aria-hidden />
        );
        const title = (
          <span className={cn("text-sm font-medium", !s.active && "text-muted-foreground")}>
            {t(`dataflowStep.${s.key}`)}
            {isEncode && <span className="ml-0.5 text-destructive">*</span>}
          </span>
        );
        const detail = s.detail ? (
          <span className="text-xs text-muted-foreground">{stepDetailLabel(s.key, s.detail, t)}</span>
        ) : null;

        // 纯信息工序:不折叠,说明/链接直接放在行尾。
        if (INLINE_STEPS.has(s.key)) {
          return (
            <div key={s.key} className="rounded-md border">
              <div className="flex items-center gap-2 px-3 py-2">
                {dot}{title}{detail}
                <span className="ml-auto">{renderStepConfig(s.key)}</span>
              </div>
            </div>
          );
        }

        return (
          <Collapsible key={s.key} defaultOpen={isEncode} className={cn("rounded-md border", isEncode && "border-primary/40")}>
            <CollapsibleTrigger className="flex w-full items-center gap-2 px-3 py-2 text-left [&[data-state=open]>svg]:rotate-180">
              {dot}{title}{detail}
              <ChevronDown className="ml-auto size-4 shrink-0 text-muted-foreground transition-transform" />
            </CollapsibleTrigger>
            <CollapsibleContent>
              <div className="border-t px-3 py-3">{renderStepConfig(s.key)}</div>
            </CollapsibleContent>
          </Collapsible>
        );
      })}
    </div>
  );
}

function stepDetailLabel(key: string, detail: string, t: ReturnType<typeof useTranslations<"channels">>): string {
  switch (key) {
    case "model_mapping": return t("dataflowDetailMappings", { count: Number(detail) || 0 });
    case "role_mapping": return t("dataflowDetailRules", { count: Number(detail) || 0 });
    case "param_override":
    case "header_override": return t("dataflowDetailFields", { fields: detail });
    case "encode": return detail;
    default: return detail;
  }
}

function ThinkingRulesEditor({ form, setForm }: { form: ChannelForm; setForm: (n: ChannelForm) => void }) {
  const t = useTranslations("channels");
  const otherSettings = parseOtherSettings(form.other_settings);
  const protos = channelProtocols(form.endpoints);
  const update = (patch: Parameters<typeof stringifyOtherSettings>[0]) => setForm({ ...form, other_settings: stringifyOtherSettings({ ...otherSettings, ...patch }) });
  const rules = otherSettings.model_thinking_passthrough ?? [];
  return (
    <div className="space-y-3">
      {!protos.openaiChat && (
        <div className="rounded-md border border-yellow-500/40 bg-yellow-500/10 px-3 py-2 text-xs text-yellow-700 dark:text-yellow-200">{t("thinkingPassthroughOnlyOpenAIChat")}</div>
      )}
      {rules.map((rule, idx) => (
        <ThinkingPassthroughCard
          key={idx}
          rule={rule}
          onChange={(next) => { const l = [...rules]; l[idx] = next; update({ model_thinking_passthrough: l }); }}
          onDelete={() => { const l = rules.filter((_, i) => i !== idx); update({ model_thinking_passthrough: l.length === 0 ? undefined : l }); }}
        />
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => update({ model_thinking_passthrough: [...rules, { model_pattern: "", send_back_thinking: false }] })}>
        + {t("thinkingPassthroughAddRule")}
      </Button>
    </div>
  );
}
