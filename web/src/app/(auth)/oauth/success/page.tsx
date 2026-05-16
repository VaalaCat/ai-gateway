"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Loader2 } from "lucide-react";
import { STORAGE_KEYS } from "@/lib/constants";

function SuccessInner() {
  const t = useTranslations("oauth");
  const router = useRouter();
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const returnTo = params.get("return_to") ?? "/dashboard";

  useEffect(() => {
    if (!token) {
      router.replace("/login?oauth_error=invalid_state");
      return;
    }
    localStorage.setItem(STORAGE_KEYS.TOKEN, token);
    document.cookie = `${STORAGE_KEYS.TOKEN}=${token}; path=/; max-age=86400; SameSite=Lax`;
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", "/oauth/success");
    }
    router.replace(returnTo);
  }, [token, returnTo, router]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <div className="flex flex-col items-center gap-3 text-muted-foreground">
        <Loader2 className="size-6 animate-spin" />
        <p className="text-sm">{t("redirecting")}</p>
      </div>
    </div>
  );
}

export default function SuccessPage() {
  return (
    <Suspense fallback={null}>
      <SuccessInner />
    </Suspense>
  );
}
