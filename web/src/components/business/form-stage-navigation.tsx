"use client";

import { Fragment, useId, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface FormStageItem<T extends string> {
  id: T;
  title: ReactNode;
  configured: boolean;
}

export interface FormStageNavigationProps<T extends string> {
  stages: readonly FormStageItem<T>[];
  activeId: T;
  onSelect: (id: T) => void;
  ariaLabel?: string;
  configuredLabel: string;
  unconfiguredLabel: string;
}

function StageDot({ configured }: { configured: boolean }) {
  return <span className={cn("inline-block size-2 shrink-0 rounded-full border", configured ? "border-primary bg-primary" : "border-muted-foreground/40 bg-transparent")} aria-hidden />;
}

function StageButton<T extends string>({ stage, activeId, onSelect, configuredLabel, unconfiguredLabel, mobile = false }: { stage: FormStageItem<T>; activeId: T; onSelect: (id: T) => void; configuredLabel: string; unconfiguredLabel: string; mobile?: boolean }) {
  const statusId = useId();
  return <><Button type="button" variant="ghost" onClick={() => onSelect(stage.id)} onKeyDown={(event) => {
    const buttons = [...event.currentTarget.parentElement!.querySelectorAll<HTMLButtonElement>("button")];
    const current = buttons.indexOf(event.currentTarget);
    const next = event.key === "ArrowDown" || event.key === "ArrowRight" ? (current + 1) % buttons.length
      : event.key === "ArrowUp" || event.key === "ArrowLeft" ? (current - 1 + buttons.length) % buttons.length
        : event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : undefined;
    if (next === undefined) return;
    event.preventDefault();
    buttons[next]?.focus();
  }} aria-current={stage.id === activeId ? "step" : undefined} aria-describedby={statusId} data-configured={stage.configured} className={cn("min-h-11 justify-start gap-2 px-3 text-left text-sm", mobile ? "shrink-0 whitespace-nowrap" : "w-full", stage.id === activeId ? "bg-accent font-medium" : "font-normal text-muted-foreground hover:bg-accent/50")}><StageDot configured={stage.configured} />{stage.title}</Button><span id={statusId} className="sr-only">{stage.configured ? configuredLabel : unconfiguredLabel}</span></>;
}

export function FormStageNavigation<T extends string>({ stages, activeId, onSelect, ariaLabel = "Form stages", configuredLabel, unconfiguredLabel }: FormStageNavigationProps<T>) {
  return <nav aria-label={ariaLabel} className="hidden w-[200px] shrink-0 flex-col gap-0.5 border-r bg-muted/20 p-2 md:flex">{stages.map((stage) => <Fragment key={stage.id}><StageButton stage={stage} activeId={activeId} onSelect={onSelect} configuredLabel={configuredLabel} unconfiguredLabel={unconfiguredLabel} /></Fragment>)}</nav>;
}

export function FormStageNavigationMobile<T extends string>({ stages, activeId, onSelect, ariaLabel = "Form stages", configuredLabel, unconfiguredLabel }: FormStageNavigationProps<T>) {
  return <nav aria-label={ariaLabel} className="sticky top-0 z-10 -mx-2 flex gap-1 overflow-x-auto border-b bg-background/80 px-2 py-2 backdrop-blur md:hidden">{stages.map((stage) => <Fragment key={stage.id}><StageButton stage={stage} activeId={activeId} onSelect={onSelect} configuredLabel={configuredLabel} unconfiguredLabel={unconfiguredLabel} mobile /></Fragment>)}</nav>;
}
