"use client";

import { useState, type JSX } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "next/navigation";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import type { ChannelFormAdapter } from "./adapter";
import type { ChannelForm as ChannelFormShape } from "./types";
import { useChannelForm, ChannelFormMode } from "./use-channel-form";
import { isSectionAllHidden, type SectionId } from "./section-visibility";
import { LifecycleStepper, LifecycleStepperMobile, type StageNavItem } from "./lifecycle-stepper";
import { SaveBar } from "./save-bar";
import { parseEndpoints } from "./utils";
import { LegacyChannelForm } from "./legacy-form";
import { MetaSection } from "./sections/meta";
import { RoutingSection } from "./sections/routing";
import { ProcessingSection } from "./sections/processing";
import { ConnectionSection } from "./sections/connection";
import { ResilienceSection } from "./sections/resilience";
import { ResponseSection } from "./sections/response";

export interface ChannelFormProps<Entity = unknown> {
  mode: ChannelFormMode;
  adapter: ChannelFormAdapter<Entity>;
  agentId?: string;
}

const STAGES: ReadonlyArray<{ id: SectionId; titleKey: string; descKey: string }> = [
  { id: "meta", titleKey: "stageMeta", descKey: "stageMetaDesc" },
  { id: "routing", titleKey: "stageRouting", descKey: "stageRoutingDesc" },
  { id: "processing", titleKey: "stageProcessing", descKey: "stageProcessingDesc" },
  { id: "connection", titleKey: "stageConnection", descKey: "stageConnectionDesc" },
  { id: "resilience", titleKey: "stageResilience", descKey: "stageResilienceDesc" },
  { id: "response", titleKey: "stageResponse", descKey: "stageResponseDesc" },
];

function stageConfigured(id: SectionId, form: ChannelFormShape): boolean {
  switch (id) {
    case "meta":
    case "routing":
    case "processing":
      return true;
    case "connection":
      return !!(form.organization || form.api_version || form.proxy_url || form.disable_keepalive);
    case "resilience":
      return form.resilience !== "";
    case "response":
      return form.status_code_mapping !== "" || form.free || (form.price_ratio !== "" && form.price_ratio !== "1");
  }
}

export function ChannelForm<Entity>({ mode, adapter, agentId }: ChannelFormProps<Entity>) {
  const t = useTranslations("channels");
  const router = useRouter();
  const { data: channelTypes = [] } = adapter.useTypes();
  const state = useChannelForm(mode, adapter);
  const visibleStages = STAGES.filter((s) => !isSectionAllHidden(s.id, adapter.hiddenFields));
  const [activeId, setActiveId] = useState<SectionId>((visibleStages[0]?.id ?? "meta") as SectionId);

  if (mode.kind === "edit" && state.notFound) {
    return (
      <div className="flex flex-col items-center justify-center gap-4 py-20">
        <p className="text-lg text-muted-foreground">{t("channelNotFound")}</p>
        <Button onClick={() => router.push(adapter.listPath)}>{t("backToList")}</Button>
      </div>
    );
  }

  if (state.isLoading) {
    return (
      <div className="grid overflow-hidden rounded-lg border md:grid-cols-[200px_1fr]">
        <div className="hidden space-y-2 bg-muted/30 p-3 md:block">
          {Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-9" />)}
        </div>
        <div className="space-y-4 p-6"><Skeleton className="h-6 w-1/3" /><Skeleton className="h-9" /><Skeleton className="h-9" /><Skeleton className="h-9" /></div>
      </div>
    );
  }

  if (state.form.use_legacy_adaptor) {
    return (
      <div className="space-y-4">
        <div className="overflow-hidden rounded-lg border bg-background shadow-sm">
          <div className="p-6">
            <LegacyChannelForm form={state.form} setForm={state.setForm} channelTypes={channelTypes} showStatus={mode.kind === "edit"} agentId={agentId} channelId={mode.kind === "edit" ? mode.id : undefined} />
          </div>
        </div>
        <SaveBar isDirty={state.isDirty} dirtyFieldCount={state.dirtyFieldCount} saving={state.saving} onSave={state.submit} onCancel={state.cancel} />
      </div>
    );
  }

  const channelId = mode.kind === "edit" ? mode.id : undefined;
  const renderStagePanel = (id: SectionId): JSX.Element => {
    switch (id) {
      case "meta":
        return <MetaSection<Entity> form={state.form} setForm={state.setForm} channelTypes={channelTypes} hiddenFields={adapter.hiddenFields} keyFieldHelpText={adapter.keyFieldHelpText} entity={state.entity} />;
      case "routing":
        return <RoutingSection form={state.form} setForm={state.setForm} agentId={agentId} useModelsCatalog={adapter.useModelsCatalog} hiddenFields={adapter.hiddenFields} showStatus={mode.kind === "edit"} channelId={channelId} />;
      case "processing":
        return <ProcessingSection form={state.form} setForm={state.setForm} channelId={channelId} hiddenFields={adapter.hiddenFields} scriptsHref={adapter.scriptsHref} />;
      case "connection":
        return <ConnectionSection form={state.form} setForm={state.setForm} hiddenFields={adapter.hiddenFields} />;
      case "resilience":
        return <ResilienceSection form={state.form} setForm={state.setForm} />;
      case "response":
        return <ResponseSection form={state.form} setForm={state.setForm} hiddenFields={adapter.hiddenFields} />;
    }
  };

  const stages: StageNavItem[] = visibleStages.map((s) => ({ id: s.id, titleKey: s.titleKey, configured: stageConfigured(s.id, state.form) }));
  const activeStage = STAGES.find((s) => s.id === activeId) ?? STAGES[0];
  // 上游协议必填:至少配置一个端点,否则禁止保存。
  const endpointMissing = Object.keys(parseEndpoints(state.form.endpoints)).length === 0;

  return (
    <div className="space-y-4">
      {/* 移动端 strip 必须在带边框卡片之外:卡片有 overflow-hidden,会让 sticky 失效。 */}
      <LifecycleStepperMobile stages={stages} activeId={activeId} onSelect={setActiveId} />
      <div className="overflow-hidden rounded-lg border bg-background shadow-sm md:flex">
        <LifecycleStepper stages={stages} activeId={activeId} onSelect={setActiveId} />
        <div className="min-w-0 flex-1 space-y-6 p-6">
          <header className="space-y-1">
            <h2 className="text-lg font-semibold tracking-tight">{t(activeStage.titleKey)}</h2>
            <p className="text-sm text-muted-foreground">{t(activeStage.descKey)}</p>
          </header>
          {renderStagePanel(activeId)}
        </div>
      </div>
      <SaveBar isDirty={state.isDirty} dirtyFieldCount={state.dirtyFieldCount} saving={state.saving} onSave={state.submit} onCancel={state.cancel} blockReason={endpointMissing ? t("encodeEndpointsRequired") : undefined} />
    </div>
  );
}
