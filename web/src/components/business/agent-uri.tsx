"use client";

import { Copy } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

export function AgentURI({ uri }: { uri: string }) {
  const t = useTranslations("agents.connection");
  if (!uri) return <span className="text-sm text-muted-foreground">-</span>;

  const split = Math.max(0, uri.length - 24);
  return (
    <div className="flex min-w-0 max-w-full items-center gap-1.5">
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="font-datatype flex min-w-0 flex-1 cursor-default text-xs" aria-label={uri}>
            <span data-slot="uri-prefix" className="min-w-0 truncate">{uri.slice(0, split)}</span>
            <span data-slot="uri-suffix" className="shrink-0">{uri.slice(split)}</span>
            <span className="sr-only truncate">{uri}</span>
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-sm break-all">{uri}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("copyUri")}
            onClick={() => void copyTextWithFeedback(uri, { success: t("copied"), error: t("copyFailed") })}
          >
            <Copy data-icon="inline-start" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("copyUri")}</TooltipContent>
      </Tooltip>
    </div>
  );
}
