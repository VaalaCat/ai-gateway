"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";

import { Skeleton } from "@/components/ui/skeleton";

import { PageLayout, type PageLayoutMaxWidth } from "./page-layout";

export interface PageLayoutSkeletonProps {
  title?: ReactNode;
  description?: ReactNode;
  maxWidth?: PageLayoutMaxWidth;
}

export function PageLayoutSkeleton({
  title,
  description,
  maxWidth = "full",
}: PageLayoutSkeletonProps) {
  const t = useTranslations("common");

  return (
    <PageLayout
      title={title ?? t("loading")}
      description={description}
      maxWidth={maxWidth}
    >
      <div
        className="flex flex-col gap-4"
        role="status"
        aria-label={t("loading")}
      >
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    </PageLayout>
  );
}
