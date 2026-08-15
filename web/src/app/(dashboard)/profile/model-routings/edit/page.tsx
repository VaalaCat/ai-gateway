"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { RoutingForm } from "@/components/model-routing/routing-form";
import { PageLayout } from "@/components/layout/page-layout";

export default function Page() {
  const t = useTranslations("modelRoutings");
  const tc = useTranslations("common");
  return (
    <PageLayout title={t("editTitle")} maxWidth="full">
      <Suspense fallback={<div className="text-muted-foreground">{tc("loading")}</div>}>
        <Inner />
      </Suspense>
    </PageLayout>
  );
}

function Inner() {
  const params = useSearchParams();
  const raw = params.get("id");
  const id = raw === null ? NaN : Number(raw);
  if (!Number.isFinite(id) || id <= 0) {
    return <div className="text-destructive">Invalid id</div>;
  }
  return <RoutingForm mode={{ kind: "edit", id }} apiMode="user" />;
}
