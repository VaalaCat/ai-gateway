"use client";

import { RoutingForm } from "@/components/model-routing/routing-form";
import { PageLayout } from "@/components/layout/page-layout";
import { useTranslations } from "next-intl";

export default function Page() {
  const t = useTranslations("modelRoutings");
  return <PageLayout title={t("newTitle")} maxWidth="full"><RoutingForm mode={{ kind: "new" }} apiMode="user" /></PageLayout>;
}
