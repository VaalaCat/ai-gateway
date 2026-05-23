"use client";

import { useTranslations } from "next-intl";
import { PageLayout } from "@/components/layout/page-layout";
import { ChannelForm } from "@/components/channel/channel-form";
import { byokChannelAdapter } from "@/components/channel/channel-form/adapters/byok";

export default function BYOKNewPage() {
  const t = useTranslations("byok");
  return (
    <PageLayout
      title={t("newSheetTitle")}
      description={t("sheetDescription")}
      maxWidth="3xl"
    >
      <ChannelForm mode={{ kind: "create" }} adapter={byokChannelAdapter} />
    </PageLayout>
  );
}
