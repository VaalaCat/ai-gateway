"use client";

import { useTranslations } from "next-intl";
import { CircleAlert } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { ChannelDisableState } from "@/lib/types";
import { formatDate } from "@/lib/utils/format";

export interface AutoBanChannelLike {
  status: number;
  auto_ban_state?: ChannelDisableState;
}

interface ChannelAutoBanBadgeProps {
  channel: AutoBanChannelLike;
}

export function ChannelAutoBanBadge({ channel }: ChannelAutoBanBadgeProps) {
  const t = useTranslations("channels");
  const state = channel.auto_ban_state;

  if (channel.status === 1 || !state?.tripped) return null;

  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge variant="destructive" className="text-2xs" tabIndex={0}>
            <CircleAlert data-icon="inline-start" />
            {t("autoBanBadge")}
          </Badge>
        </TooltipTrigger>
        <TooltipContent>
          <div className="flex flex-col gap-1">
            <div>{t("autoBanTrippedDescription")}</div>
            {state.tripped_at ? (
              <div>
                {t("autoBanTrippedAt")} {formatDate(state.tripped_at)}
              </div>
            ) : null}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
