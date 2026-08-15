"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";

import { EntityInsightView } from "@/components/business/entity-insight/view";
import { PageLayout } from "@/components/layout/page-layout";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import {
  INSIGHT_REGISTRY,
  type EntityType,
} from "@/components/business/entity-insight/registry";

function InsightPageSkeleton() {
  const t = useTranslations("monitoring");

  return (
    <PageLayoutSkeleton
      title={t("title")}
      description={t("subtitle")}
      maxWidth="full"
    />
  );
}

export default function EntityInsightPage() {
  return (
    <Suspense fallback={<InsightPageSkeleton />}>
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const t = useTranslations("insights");
  const tm = useTranslations("monitoring");
  const sp = useSearchParams();
  const typeRaw = (sp.get("type") ?? "agent") as EntityType;
  const id = sp.get("id");

  if (!id) {
    return (
      <PageLayout title={tm("title")} description={tm("subtitle")} maxWidth="full">
        <p className="py-12 text-center text-destructive">{t("missingId")}</p>
      </PageLayout>
    );
  }
  const cfg = INSIGHT_REGISTRY[typeRaw];
  if (!cfg) {
    return (
      <PageLayout title={tm("title")} description={tm("subtitle")} maxWidth="full">
        <p className="py-12 text-center text-destructive">
          {t("unknownType", { type: typeRaw })}
        </p>
      </PageLayout>
    );
  }
  if (cfg.stage === "planned") {
    return (
      <PageLayout title={t(`title.${typeRaw}`)} description={id} maxWidth="full">
        <p className="py-12 text-center text-muted-foreground">{t("notImplemented")}</p>
      </PageLayout>
    );
  }
  return <EntityInsightView type={typeRaw} id={id} cfg={cfg} />;
}
