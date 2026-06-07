"use client";

import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";
import type { SectionId } from "./section-visibility";

export interface StageNavItem {
  id: SectionId;
  titleKey: string;
  /** 本 channel 该阶段是否有生效配置;false → 节点灰显空心。 */
  configured: boolean;
}

export interface LifecycleStepperProps {
  stages: StageNavItem[];
  activeId: SectionId;
  onSelect: (id: SectionId) => void;
}

function Dot({ configured }: { configured: boolean }) {
  return (
    <span
      className={cn(
        "inline-block size-2 shrink-0 rounded-full border",
        configured
          ? "border-primary bg-primary"
          : "border-muted-foreground/40 bg-transparent",
      )}
      aria-hidden
    />
  );
}

/**
 * 移动端顶部横向可滚动 strip。必须由 index 渲染在带边框卡片**之外**,否则
 * 任何 overflow:hidden 祖先都会让 `sticky` 失效。
 */
export function LifecycleStepperMobile({ stages, activeId, onSelect }: LifecycleStepperProps) {
  const t = useTranslations("channels");

  return (
    <div className="sticky top-0 z-10 -mx-2 flex gap-1 overflow-x-auto border-b bg-background/80 px-2 py-2 backdrop-blur md:hidden">
      {stages.map((s) => (
        <button
          key={s.id}
          type="button"
          onClick={() => onSelect(s.id)}
          className={cn(
            "flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm whitespace-nowrap",
            s.id === activeId ? "bg-accent font-medium" : "text-muted-foreground",
          )}
        >
          <Dot configured={s.configured} />
          {t(s.titleKey)}
        </button>
      ))}
    </div>
  );
}

/** 桌面端左侧竖向 rail。渲染在圆角卡片内,由卡片的 overflow 裁剪。 */
export function LifecycleStepper({ stages, activeId, onSelect }: LifecycleStepperProps) {
  const t = useTranslations("channels");

  return (
    <nav className="hidden w-[200px] shrink-0 flex-col gap-0.5 border-r bg-muted/20 p-2 md:flex">
      {stages.map((s) => (
        <button
          key={s.id}
          type="button"
          onClick={() => onSelect(s.id)}
          className={cn(
            "flex items-center gap-2 rounded-md px-3 py-2 text-left text-sm",
            s.id === activeId
              ? "bg-accent font-medium"
              : "font-normal text-muted-foreground hover:bg-accent/50",
          )}
        >
          <Dot configured={s.configured} />
          {t(s.titleKey)}
        </button>
      ))}
    </nav>
  );
}
