"use client";

import { useTranslations } from "next-intl";

import { Card, CardContent } from "@/components/ui/card";

export function PlannedHeader() {
  const t = useTranslations("insights");
  return (
    <Card>
      <CardContent className="py-6 text-center text-muted-foreground">
        {t("notImplemented")}
      </CardContent>
    </Card>
  );
}
