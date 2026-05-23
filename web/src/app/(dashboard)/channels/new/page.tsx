"use client";

import { useTranslations } from "next-intl";
import { PageLayout } from "@/components/layout/page-layout";
import { ChannelForm } from "@/components/channel/channel-form";
import { adminChannelAdapter } from "@/components/channel/channel-form/adapters/admin";

export default function NewChannelPage() {
  const t = useTranslations("channels");
  return (
    <PageLayout
      title={t("createTitle")}
      description={t("createDescription")}
      maxWidth="3xl"
    >
      <ChannelForm mode={{ kind: "create" }} adapter={adminChannelAdapter} />
    </PageLayout>
  );
}
