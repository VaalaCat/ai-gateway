"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { PageLayout } from "@/components/layout/page-layout";
import { ChannelForm } from "@/components/channel/channel-form";
import { byokChannelAdapter } from "@/components/channel/channel-form/adapters/byok";

export default function BYOKEditPage() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      }
    >
      <BYOKEditContent />
    </Suspense>
  );
}

function BYOKEditContent() {
  const t = useTranslations("byok");
  const router = useRouter();
  const params = useSearchParams();
  const raw = params.get("id");
  const id = raw === null ? NaN : Number(raw);
  const idValid = Number.isFinite(id) && id > 0;

  useEffect(() => {
    if (!idValid) {
      toast.error(t("notFound"));
      router.replace("/byok");
    }
  }, [idValid, router, t]);

  if (!idValid) return null;

  return (
    <PageLayout
      title={t("editSheetTitle")}
      description={t("sheetDescription")}
      maxWidth="3xl"
    >
      <ChannelForm mode={{ kind: "edit", id }} adapter={byokChannelAdapter} />
    </PageLayout>
  );
}
