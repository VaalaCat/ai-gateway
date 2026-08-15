"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { PageLayout } from "@/components/layout/page-layout";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import { ScriptForm } from "@/components/script/script-form";

function EditScriptPageSkeleton() {
  const t = useTranslations("scripts");

  return (
    <PageLayoutSkeleton
      title={t("editTitle")}
      description={t("editDescription")}
      maxWidth="full"
    />
  );
}

export default function EditScriptPage() {
  return (
    <Suspense fallback={<EditScriptPageSkeleton />}>
      <EditScriptContent />
    </Suspense>
  );
}

function EditScriptContent() {
  const t = useTranslations("scripts");
  const router = useRouter();
  const params = useSearchParams();
  const raw = params.get("id");
  const id = raw === null ? NaN : Number(raw);
  const idValid = Number.isFinite(id) && id > 0;

  useEffect(() => {
    if (!idValid) {
      toast.error(t("notFound"));
      router.replace("/scripts");
    }
  }, [idValid, router, t]);

  if (!idValid) {
    return (
      <PageLayout
        title={t("editTitle")}
        description={t("editDescription")}
        maxWidth="full"
      >
        <p className="py-12 text-center text-muted-foreground">{t("notFound")}</p>
      </PageLayout>
    );
  }

  return (
    <PageLayout title={t("editTitle")} description={t("editDescription")} maxWidth="full">
      <ScriptForm mode={{ kind: "edit", id }} />
    </PageLayout>
  );
}
