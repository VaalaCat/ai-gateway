import type * as React from "react";

import { cn } from "@/lib/utils";

export interface PageHeaderProps {
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  backAction?: React.ReactNode;
  metadata?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  title,
  description,
  actions,
  backAction,
  metadata,
  className,
}: PageHeaderProps): React.JSX.Element {
  return (
    <header
      data-slot="page-header"
      data-testid="page-header"
      className={cn(
        "flex flex-col gap-3 pb-4 md:flex-row md:items-start md:justify-between md:gap-4 md:pb-6",
        className,
      )}
    >
      <div className="flex min-w-0 items-start gap-2">
        {backAction}
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h1 className="min-w-0 text-2xl font-bold tracking-tight">
              {title}
            </h1>
            {metadata}
          </div>
          {description ? (
            <p className="text-sm text-muted-foreground">{description}</p>
          ) : null}
        </div>
      </div>
      {actions ? (
        <div
          data-testid="page-header-actions"
          className="flex flex-wrap items-center gap-2 md:shrink-0"
        >
          {actions}
        </div>
      ) : null}
    </header>
  );
}
