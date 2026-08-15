"use client";

import { useTranslations } from "next-intl";

import { FormStageNavigation, FormStageNavigationMobile } from "@/components/business/form-stage-navigation";
import type { SectionId } from "./section-visibility";

export interface StageNavItem {
  id: SectionId;
  titleKey: string;
  configured: boolean;
}

export interface LifecycleStepperProps {
  stages: StageNavItem[];
  activeId: SectionId;
  onSelect: (id: SectionId) => void;
}

function useChannelStages(stages: StageNavItem[]) {
  const t = useTranslations("channels");
  return stages.map((stage) => ({ id: stage.id, title: t(stage.titleKey), configured: stage.configured }));
}

/** Channel 仅翻译其 stage metadata；交互和可访问性统一由共享导航实现。 */
export function LifecycleStepperMobile({ stages, activeId, onSelect }: LifecycleStepperProps) {
  const t = useTranslations("channels");
  return <FormStageNavigationMobile stages={useChannelStages(stages)} activeId={activeId} onSelect={onSelect} ariaLabel={t("formStages")} configuredLabel={t("stageConfigured")} unconfiguredLabel={t("stageNotConfigured")} />;
}

export function LifecycleStepper({ stages, activeId, onSelect }: LifecycleStepperProps) {
  const t = useTranslations("channels");
  return <FormStageNavigation stages={useChannelStages(stages)} activeId={activeId} onSelect={onSelect} ariaLabel={t("formStages")} configuredLabel={t("stageConfigured")} unconfiguredLabel={t("stageNotConfigured")} />;
}
