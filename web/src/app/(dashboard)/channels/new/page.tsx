"use client";

import { useTranslations } from "next-intl";
import { ChannelForm } from "@/components/channel/channel-form";

export default function NewChannelPage() {
  const t = useTranslations("channels");
  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-2xl font-bold">{t("createTitle")}</h1>
      </header>
      <ChannelForm mode={{ kind: "create" }} />
    </div>
  );
}
