"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";

import { PageLayout } from "@/components/layout/page-layout";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import { RateLimiterForm } from "@/components/rate-limiter/rate-limiter-form";

function EditRateLimiterPageSkeleton() {
  const t = useTranslations("rateLimiters");

  return (
    <PageLayoutSkeleton
      title={t("editTitle")}
      description={t("editDescription")}
      maxWidth="3xl"
    />
  );
}

export default function EditRateLimiterPage() {
  return (
    <Suspense fallback={<EditRateLimiterPageSkeleton />}>
      <EditRateLimiterContent />
    </Suspense>
  );
}

function EditRateLimiterContent() {
  const t = useTranslations("rateLimiters");
  const router = useRouter();
  const params = useSearchParams();
  const raw = params.get("id");

  // 列表页用 ?id=new 进创建态，?id=<num> 进编辑态。
  const isCreate = raw === "new";
  const id = raw === null ? NaN : Number(raw);
  const idValid = isCreate || (Number.isFinite(id) && id > 0);

  useEffect(() => {
    if (!idValid) {
      toast.error(t("notFound"));
      router.replace("/rate-limiters");
    }
  }, [idValid, router, t]);

  if (!idValid) {
    return (
      <PageLayout
        title={t("editTitle")}
        description={t("editDescription")}
        maxWidth="3xl"
      >
        <p className="py-12 text-center text-muted-foreground">{t("notFound")}</p>
      </PageLayout>
    );
  }

  return (
    <PageLayout
      title={isCreate ? t("createTitle") : t("editTitle")}
      description={isCreate ? t("createDescription") : t("editDescription")}
      maxWidth="3xl"
    >
      <RateLimiterForm
        mode={isCreate ? { kind: "create" } : { kind: "edit", id }}
      />
    </PageLayout>
  );
}
