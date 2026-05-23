"use client";

import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type PageLayoutMaxWidth = "md" | "lg" | "2xl" | "3xl" | "5xl" | "full";

export interface PageLayoutProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  footer?: ReactNode;
  maxWidth?: PageLayoutMaxWidth;
  children: ReactNode;
}

const MAX_W_CLASS: Record<PageLayoutMaxWidth, string> = {
  md: "max-w-md",
  lg: "max-w-lg",
  "2xl": "max-w-2xl",
  "3xl": "max-w-3xl",
  "5xl": "max-w-5xl",
  full: "max-w-full",
};

export function PageLayout({
  title,
  description,
  actions,
  footer,
  maxWidth = "5xl",
  children,
}: PageLayoutProps) {
  const widthClass = MAX_W_CLASS[maxWidth];
  return (
    <div className="flex min-h-full flex-col">
      <header className="flex flex-col gap-3 pb-4 md:flex-row md:items-start md:justify-between md:gap-4 md:pb-6">
        <div className="min-w-0 space-y-1">
          <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
          {description ? (
            <p className="text-sm text-muted-foreground">{description}</p>
          ) : null}
        </div>
        {actions ? (
          <div className="flex flex-wrap items-center gap-2 md:shrink-0">
            {actions}
          </div>
        ) : null}
      </header>

      <div className="flex-1">
        <div className={cn("mx-auto w-full", widthClass)}>{children}</div>
      </div>

      {footer ? (
        // -mx-6 -mb-6 cancels the outer main p-6 from app/(dashboard)/layout.tsx so the sticky footer spans the full SidebarInset width and reaches its bottom edge.
        <div className="sticky bottom-0 z-10 -mx-6 -mb-6 mt-4 border-t bg-background/95 px-6 py-3 backdrop-blur md:py-4 pb-[max(env(safe-area-inset-bottom),0.75rem)]">
          <div className={cn("mx-auto w-full", widthClass)}>
            <div className="flex flex-col-reverse gap-2 md:flex-row md:flex-wrap md:justify-end">
              {footer}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
