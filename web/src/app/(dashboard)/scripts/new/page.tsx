"use client";

import { useTranslations } from "next-intl";
import { PageLayout } from "@/components/layout/page-layout";
import { ScriptForm } from "@/components/script/script-form";

export default function NewScriptPage() {
  const t = useTranslations("scripts");
  return (
    <PageLayout title={t("createTitle")} description={t("createDescription")} maxWidth="full">
      <ScriptForm mode={{ kind: "create" }} />
    </PageLayout>
  );
}
