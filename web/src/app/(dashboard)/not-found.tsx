"use client";

import { useTranslations } from "next-intl";

import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

export default function DashboardNotFound() {
  const t = useTranslations("common");

  return (
    <PageLayout title={t("notFound")} maxWidth="full">
      <Alert>
        <AlertTitle>{t("notFound")}</AlertTitle>
        <AlertDescription>{t("notFoundDescription")}</AlertDescription>
      </Alert>
    </PageLayout>
  );
}
