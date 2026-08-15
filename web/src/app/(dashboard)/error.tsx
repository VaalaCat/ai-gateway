"use client";

import { useTranslations } from "next-intl";

import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

interface DashboardErrorProps {
  error: Error & { digest?: string };
  reset: () => void;
}

export default function DashboardError({ error, reset }: DashboardErrorProps) {
  const t = useTranslations("common");

  return (
    <PageLayout title={t("error")} maxWidth="full">
      <Alert variant="destructive" data-error-digest={error.digest}>
        <AlertTitle>{t("error")}</AlertTitle>
        <AlertDescription>{t("errorDescription")}</AlertDescription>
        <Button type="button" variant="outline" className="mt-4" onClick={reset}>
          {t("retry")}
        </Button>
      </Alert>
    </PageLayout>
  );
}
