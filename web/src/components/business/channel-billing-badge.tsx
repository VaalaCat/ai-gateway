"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { Channel } from "@/lib/types";
import { formatFactor } from "@/lib/utils/format";

// 判别联合:discount/markup 必带 ratio(number),好让消费方/`t()` 拿到非可选 number。
export type BillingBadge =
  | { kind: "free" }
  | { kind: "none" }
  | { kind: "discount" | "markup"; ratio: number };

// 纯判定:免费优先于倍率;price_ratio 为 0/1/未设 = 原价,不展示。
// price_ratio 类型为 number | undefined(后端范围 0..1000),故不判 null。
export function billingBadge(
  channel: Pick<Channel, "free" | "price_ratio">,
): BillingBadge {
  if (channel.free) return { kind: "free" };
  const r = channel.price_ratio;
  if (r === undefined || r === 0 || r === 1) return { kind: "none" };
  if (r < 1) return { kind: "discount", ratio: r };
  return { kind: "markup", ratio: r };
}

interface ChannelBillingBadgeProps {
  channel: Channel;
}

export function ChannelBillingBadge({ channel }: ChannelBillingBadgeProps) {
  const t = useTranslations("channels");
  const b = billingBadge(channel);

  if (b.kind === "none") return null;

  if (b.kind === "free") {
    return (
      <Badge
        variant="outline"
        className="gap-1 text-2xs border-green-500/50 text-green-600 dark:text-green-400"
      >
        {t("billingFree")}
      </Badge>
    );
  }

  const isDiscount = b.kind === "discount";
  const badge = (
    <Badge
      variant="outline"
      className={`gap-1 text-2xs ${
        isDiscount
          ? "border-sky-500/50 text-sky-600 dark:text-sky-400"
          : "border-amber-500/50 text-amber-600 dark:text-amber-400"
      }`}
    >
      ×{formatFactor(b.ratio)}
    </Badge>
  );

  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>{badge}</TooltipTrigger>
        <TooltipContent>{t("priceDiscountTip", { ratio: formatFactor(b.ratio) })}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
