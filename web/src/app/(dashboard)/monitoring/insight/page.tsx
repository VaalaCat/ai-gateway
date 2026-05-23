"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";

import { EntityInsightView } from "@/components/business/entity-insight/view";
import {
  INSIGHT_REGISTRY,
  type EntityType,
} from "@/components/business/entity-insight/registry";

export default function EntityInsightPage() {
  return (
    <Suspense
      fallback={
        <div className="py-12 text-center text-muted-foreground">Loading...</div>
      }
    >
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const t = useTranslations("insights");
  const sp = useSearchParams();
  const typeRaw = (sp.get("type") ?? "agent") as EntityType;
  const id = sp.get("id");

  if (!id) {
    return (
      <div className="py-12 text-center text-destructive">
        {t("missingId")}
      </div>
    );
  }
  const cfg = INSIGHT_REGISTRY[typeRaw];
  if (!cfg) {
    return (
      <div className="py-12 text-center text-destructive">
        {t("unknownType", { type: typeRaw })}
      </div>
    );
  }
  if (cfg.stage === "planned") {
    return (
      <div className="py-12 text-center text-muted-foreground">
        {t("notImplemented")}
      </div>
    );
  }
  return <EntityInsightView type={typeRaw} id={id} cfg={cfg} />;
}
