"use client";

import { useTranslations } from "next-intl";
import { Lock, Hand } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { formatDate } from "@/lib/utils/format";
import type { Channel } from "@/lib/types";

interface ChannelLimitBadgeProps {
  channel: Channel;
}

// reason 机读 kind("cutoff" / "<metric>/<window>")→ i18n 文案
function reasonText(t: (k: string) => string, reason?: string): string {
  if (!reason) return "";
  if (reason === "cutoff") return t("limitReasonCutoff");
  const metric = reason.split("/")[0];
  if (metric === "calls") return t("limitReasonCalls");
  if (metric === "cost") return t("limitReasonCost");
  return reason;
}

export function ChannelLimitBadge({ channel }: ChannelLimitBadgeProps) {
  const t = useTranslations("channels");
  if (channel.status === 1) return null; // 启用态不显 badge

  const state = channel.limit_state;
  const auto = !!state?.tripped;

  const badge = (
    <Badge variant="outline" className="gap-1 text-2xs">
      {auto ? <Lock className="size-3" /> : <Hand className="size-3" />}
      {auto ? t("limitBadgeAuto") : t("limitBadgeManual")}
    </Badge>
  );

  if (!auto) return badge; // 手动禁用:无运行态细节,只标手动

  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>{badge}</TooltipTrigger>
        <TooltipContent>
          <div className="space-y-1 text-xs">
            <div className="font-medium">{reasonText(t, state?.reason)}</div>
            {state?.tripped_at ? (
              <div>
                {t("limitTrippedAt")} {formatDate(state.tripped_at)}
              </div>
            ) : null}
            <div>
              {state?.auto_recover ? t("limitWillRecover") : t("limitPermanent")}
            </div>
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
