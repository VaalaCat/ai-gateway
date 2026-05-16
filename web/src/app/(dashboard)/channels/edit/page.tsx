"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { ChannelForm } from "@/components/channel/channel-form";
import { useAgentRoutes } from "@/lib/api/agent-routes";

export default function EditChannelPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>}>
      <EditChannelContent />
    </Suspense>
  );
}

function EditChannelContent() {
  const t = useTranslations("channels");
  const router = useRouter();
  const params = useSearchParams();
  const raw = params.get("id");
  const id = raw === null ? NaN : Number(raw);
  const idValid = Number.isFinite(id) && id > 0;

  // Resolve default agent id for fetch-models the same way the dialog used to.
  const { data: agentRoutes } = useAgentRoutes(
    idValid ? { source_type: "channel" as const, source_id: id } : {}
  );
  const defaultAgentId = idValid
    ? (agentRoutes?.data ?? []).find((r) => !r.model)?.agent_id
    : undefined;

  useEffect(() => {
    if (!idValid) {
      toast.error(t("channelNotFound"));
      router.replace("/channels");
    }
  }, [idValid, router, t]);

  if (!idValid) return null;

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-2xl font-bold">{t("editTitle")}</h1>
      </header>
      <ChannelForm mode={{ kind: "edit", id }} agentId={defaultAgentId} />
    </div>
  );
}
