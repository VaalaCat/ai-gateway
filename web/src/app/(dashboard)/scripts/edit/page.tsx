"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { PageLayout } from "@/components/layout/page-layout";
import { ScriptForm } from "@/components/script/script-form";

export default function EditScriptPage() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      }
    >
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

  if (!idValid) return null;

  return (
    <PageLayout title={t("editTitle")} description={t("editDescription")} maxWidth="full">
      <ScriptForm mode={{ kind: "edit", id }} />
    </PageLayout>
  );
}
