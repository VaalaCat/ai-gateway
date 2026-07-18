"use client";

import { Badge } from "@/components/ui/badge";
import { useTranslations } from "next-intl";

export function ScopeBadge({ scope }: { scope: "global" | "user" | "token" }) {
  const t = useTranslations("modelRoutings");
  return (
    <Badge variant={scope === "global" ? "secondary" : scope === "user" ? "outline" : "default"}>
      {t(`scope.${scope}`)}
    </Badge>
  );
}
