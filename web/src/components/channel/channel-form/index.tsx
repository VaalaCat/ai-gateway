"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "next/navigation";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ChannelFormAdapter } from "./adapter";
import { useChannelForm, ChannelFormMode } from "./use-channel-form";
import { isSectionAllHidden, type SectionId } from "./section-visibility";
import { SaveBar } from "./save-bar";
import { LegacyChannelForm } from "./legacy-form";
import { BasicSection } from "./sections/basic";
import { EndpointsProtocolSection } from "./sections/endpoints-protocol";
import { ModelsSection } from "./sections/models";
import { ProtocolBehaviorSection } from "./sections/protocol-behavior";
import { RequestRewriteSection } from "./sections/request-rewrite";
import { RouteRolesSection } from "./sections/route-roles";

export interface ChannelFormProps<Entity = unknown> {
  mode: ChannelFormMode;
  adapter: ChannelFormAdapter<Entity>;
  /** Resolved default agent id for fetch-models in edit mode (optional). */
  agentId?: string;
}

const SECTIONS: ReadonlyArray<{
  id: SectionId;
  number: string;
  titleKey: string;
  descKey: string;
}> = [
  { id: "basic", number: "①", titleKey: "sectionBasic", descKey: "sectionBasicDescription" },
  { id: "endpoints-protocol", number: "②", titleKey: "sectionEndpointsProtocol", descKey: "sectionEndpointsProtocolDescription" },
  { id: "models", number: "③", titleKey: "sectionModels", descKey: "sectionModelsDescription" },
  { id: "protocol-behavior", number: "④", titleKey: "sectionProtocolBehavior", descKey: "sectionProtocolBehaviorDescription" },
  { id: "request-rewrite", number: "⑤", titleKey: "sectionRequestRewrite", descKey: "sectionRequestRewriteDescription" },
  { id: "route-roles", number: "⑥", titleKey: "sectionRouteRoles", descKey: "sectionRouteRolesDescription" },
];

export function ChannelForm<Entity>({
  mode,
  adapter,
  agentId,
}: ChannelFormProps<Entity>) {
  const t = useTranslations("channels");
  const router = useRouter();
  const { data: channelTypes = [] } = adapter.useTypes();
  const state = useChannelForm(mode, adapter);
  const visibleSections = SECTIONS.filter(
    (s) => !isSectionAllHidden(s.id, adapter.hiddenFields),
  );
  const [activeId, setActiveId] = useState<SectionId>(
    (visibleSections[0]?.id ?? "basic") as SectionId,
  );

  if (mode.kind === "edit" && state.notFound) {
    return (
      <div className="flex flex-col items-center justify-center gap-4 py-20">
        <p className="text-lg text-muted-foreground">{t("channelNotFound")}</p>
        <Button onClick={() => router.push(adapter.listPath)}>
          {t("backToList")}
        </Button>
      </div>
    );
  }

  if (state.isLoading) {
    return (
      <div className="grid overflow-hidden rounded-lg border md:grid-cols-[200px_1fr]">
        <div className="hidden space-y-2 bg-muted/30 p-3 md:block">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-9" />
          ))}
        </div>
        <div className="space-y-4 p-6">
          <Skeleton className="h-6 w-1/3" />
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
        </div>
      </div>
    );
  }

  // Legacy mode keeps its own collapsible layout untouched.
  if (state.form.use_legacy_adaptor) {
    return (
      <div className="space-y-4">
        <div className="overflow-hidden rounded-lg border bg-background shadow-sm">
          <div className="p-6">
            <LegacyChannelForm
              form={state.form}
              setForm={state.setForm}
              channelTypes={channelTypes}
              showStatus={mode.kind === "edit"}
              agentId={agentId}
              channelId={mode.kind === "edit" ? mode.id : undefined}
            />
          </div>
        </div>
        <SaveBar
          isDirty={state.isDirty}
          dirtyFieldCount={state.dirtyFieldCount}
          saving={state.saving}
          onSave={state.submit}
          onCancel={state.cancel}
        />
      </div>
    );
  }

  const renderSectionBody = (id: SectionId) => {
    switch (id) {
      case "basic":
        return (
          <BasicSection
            form={state.form}
            setForm={state.setForm}
            channelTypes={channelTypes}
            showStatus={mode.kind === "edit"}
            hiddenFields={adapter.hiddenFields}
            keyFieldHelpText={adapter.keyFieldHelpText}
            entity={state.entity}
          />
        );
      case "endpoints-protocol":
        return (
          <EndpointsProtocolSection form={state.form} setForm={state.setForm} />
        );
      case "models":
        return (
          <ModelsSection
            form={state.form}
            setForm={state.setForm}
            agentId={agentId}
            useModelsCatalog={adapter.useModelsCatalog}
          />
        );
      case "protocol-behavior":
        return (
          <ProtocolBehaviorSection form={state.form} setForm={state.setForm} />
        );
      case "request-rewrite":
        return (
          <RequestRewriteSection
            form={state.form}
            setForm={state.setForm}
            hiddenFields={adapter.hiddenFields}
          />
        );
      case "route-roles":
        return (
          <RouteRolesSection
            form={state.form}
            setForm={state.setForm}
            channelId={mode.kind === "edit" ? mode.id : undefined}
          />
        );
    }
  };

  const handleSelect = (v: string) => setActiveId(v as SectionId);

  return (
    <div className="space-y-4">
      {/* Mobile section picker — independent of Tabs.Root, just shares activeId state. */}
      <div className="sticky top-0 z-10 -mx-2 border-b bg-background/80 px-2 py-2 backdrop-blur md:hidden">
        <Select value={activeId} onValueChange={handleSelect}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {visibleSections.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                <span className="mr-2 tabular-nums text-muted-foreground">
                  {s.number}
                </span>
                {t(s.titleKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="overflow-hidden rounded-lg border bg-background shadow-sm">
        <Tabs
          value={activeId}
          onValueChange={handleSelect}
          orientation="vertical"
          className="gap-0"
        >
          {/* Desktop tab list (vertical). Hidden on mobile — picker handles switching there. */}
          <TabsList
            variant="line"
            className="hidden h-auto w-[200px] shrink-0 flex-col items-stretch justify-start gap-0.5 border-r bg-muted/20 p-2 md:flex"
          >
            {visibleSections.map((s) => (
              <TabsTrigger
                key={s.id}
                value={s.id}
                className="justify-start font-normal data-[state=active]:font-medium"
              >
                <span className="mr-2 tabular-nums text-muted-foreground">
                  {s.number}
                </span>
                {t(s.titleKey)}
              </TabsTrigger>
            ))}
          </TabsList>

          <div className="min-w-0 flex-1 overflow-x-hidden">
            {visibleSections.map((s) => (
              <TabsContent
                key={s.id}
                value={s.id}
                className="mt-0 space-y-6 p-6 outline-hidden"
              >
                <header className="space-y-1">
                  <h2 className="text-lg font-semibold tracking-tight">
                    {t(s.titleKey)}
                  </h2>
                  <p className="text-sm text-muted-foreground">
                    {t(s.descKey)}
                  </p>
                </header>
                {renderSectionBody(s.id)}
              </TabsContent>
            ))}
          </div>
        </Tabs>
      </div>

      <SaveBar
        isDirty={state.isDirty}
        dirtyFieldCount={state.dirtyFieldCount}
        saving={state.saving}
        onSave={state.submit}
        onCancel={state.cancel}
      />
    </div>
  );
}
