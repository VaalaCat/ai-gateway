"use client";

import { Suspense, useEffect } from "react";
import { useTranslations } from "next-intl";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";

import { PageLayout } from "@/components/layout/page-layout";
import { RateLimiterForm } from "@/components/rate-limiter/rate-limiter-form";

export default function EditRateLimiterPage() {
  return (
    <Suspense
      fallback={
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      }
    >
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

  if (!idValid) return null;

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
