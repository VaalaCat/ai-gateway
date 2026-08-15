"use client";

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import { PageHeader } from "./page-header";

export type PageLayoutMaxWidth = "md" | "lg" | "2xl" | "3xl" | "5xl" | "full";

export interface PageLayoutProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  backAction?: ReactNode;
  metadata?: ReactNode;
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
  backAction,
  metadata,
  footer,
  maxWidth = "5xl",
  children,
}: PageLayoutProps) {
  const widthClass = MAX_W_CLASS[maxWidth];
  return (
    <div className="flex min-h-full flex-col">
      <PageHeader
        title={title}
        description={description}
        actions={actions}
        backAction={backAction}
        metadata={metadata}
      />

      <div
        data-slot="page-layout-content"
        data-testid="page-layout-content"
        className="flex-1"
      >
        <div className={cn("mx-auto w-full", widthClass)}>{children}</div>
      </div>

      {footer ? (
        <div
          data-slot="page-layout-footer"
          data-testid="page-layout-footer"
          className={cn(
            "sticky bottom-[var(--dashboard-bottom-nav-offset)] z-20 mx-auto mt-4 flex w-full flex-wrap gap-2 border-t bg-background/95 py-3 pb-[max(env(safe-area-inset-bottom),0.75rem)] backdrop-blur md:justify-end md:py-4 max-sm:[&>[data-slot=button]]:flex-1",
            widthClass,
          )}
        >
          {footer}
        </div>
      ) : null}
    </div>
  );
}
