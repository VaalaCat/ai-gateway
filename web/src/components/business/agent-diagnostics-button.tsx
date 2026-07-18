"use client";

import { ClipboardCopy, LoaderCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useAgentConnectionDiagnostics } from "@/lib/api/agents";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

export function AgentDiagnosticsButton({ agentId }: { agentId: number }) {
  const t = useTranslations("agents.connection");
  const diagnostics = useAgentConnectionDiagnostics(agentId, { enabled: false });

  const copyDiagnostics = async () => {
    const result = await diagnostics.refetch();
    if (!result.data) {
      toast.error(t("diagnosticsLoadFailed"));
      return;
    }
    await copyTextWithFeedback(JSON.stringify(result.data, null, 2), {
      success: t("diagnosticsCopied"),
      error: t("diagnosticsCopyFailed"),
    });
  };

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="icon-sm"
          className="size-11 sm:size-8"
          aria-label={t("copyDiagnostics")}
          disabled={diagnostics.isFetching}
          onClick={() => void copyDiagnostics()}
        >
          {diagnostics.isFetching
            ? <LoaderCircle data-icon="inline-start" className="animate-spin" />
            : <ClipboardCopy data-icon="inline-start" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{t("copyDiagnostics")}</TooltipContent>
    </Tooltip>
  );
}
