"use client";

import { Copy } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

export function AgentURI({ uri }: { uri: string }) {
  const t = useTranslations("agents.connection");
  if (!uri) return <span className="text-sm text-muted-foreground">-</span>;

  const copyURI = () => void copyTextWithFeedback(uri, {
    success: t("copied"),
    error: t("copyFailed"),
  });
  return (
    <Popover>
      <div className="flex min-w-0 max-w-full items-center gap-1.5">
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="font-datatype min-w-0 flex-1 shrink justify-start truncate text-left"
          >
            <span className="block min-w-0 flex-1 truncate text-left">{uri}</span>
          </Button>
        </PopoverTrigger>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("copyUri")}
          onClick={copyURI}
        >
          <Copy data-icon="inline-start" />
        </Button>
      </div>
      <PopoverContent
        align="start"
        aria-label={t("relayUri")}
        className="max-h-(--radix-popover-content-available-height) w-[min(22rem,calc(100vw-2rem))] overflow-y-auto"
      >
        <div className="flex min-w-0 items-start gap-1.5">
          <code className="min-w-0 flex-1 select-all break-all font-mono text-xs">{uri}</code>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("copyUri")}
            onClick={copyURI}
          >
            <Copy data-icon="inline-start" />
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
