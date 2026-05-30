"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { PageLayout } from "@/components/layout/page-layout";
import { ChannelForm } from "@/components/channel/channel-form";
import { adminChannelAdapter } from "@/components/channel/channel-form/adapters/admin";

function NewChannelInner() {
  const t = useTranslations("channels");
  const sp = useSearchParams();
  const from = sp.get("from");
  const mode = from
    ? ({ kind: "copy", id: Number(from) } as const)
    : ({ kind: "create" } as const);
  return (
    <PageLayout
      title={from ? t("copyTitle") : t("createTitle")}
      description={t("createDescription")}
      maxWidth="3xl"
    >
      <ChannelForm mode={mode} adapter={adminChannelAdapter} />
    </PageLayout>
  );
}

export default function NewChannelPage() {
  return (
    <Suspense fallback={<div className="py-12 text-center text-muted-foreground">Loading...</div>}>
      <NewChannelInner />
    </Suspense>
  );
}
