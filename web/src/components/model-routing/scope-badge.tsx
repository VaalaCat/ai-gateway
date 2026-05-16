"use client";

import { Badge } from "@/components/ui/badge";
import { useTranslations } from "next-intl";

export function ScopeBadge({ scope }: { scope: 'global' | 'user' }) {
  const t = useTranslations("modelRoutings");
  if (scope === 'global') {
    return (
      <Badge className="bg-indigo-50 text-indigo-700 hover:bg-indigo-50 dark:bg-indigo-950/40 dark:text-indigo-300">
        {t("scope.global")}
      </Badge>
    );
  }
  return (
    <Badge className="bg-emerald-50 text-emerald-700 hover:bg-emerald-50 dark:bg-emerald-950/40 dark:text-emerald-300">
      {t("scope.user")}
    </Badge>
  );
}
