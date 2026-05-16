"use client";

import { Suspense, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { KeyRound, Loader2 } from "lucide-react";
import { oauthApi } from "@/lib/api/oauth";
import { ApiError } from "@/lib/api/client";
import { decodeJwtPayload } from "@/lib/jwt";
import { STORAGE_KEYS } from "@/lib/constants";

interface BindClaims {
  provider_id?: number;
  email?: string;
  display_name?: string;
  suggested_username?: string;
}

function ChoosePageInner() {
  const t = useTranslations("oauth");
  const router = useRouter();
  const params = useSearchParams();
  const ticket = params.get("ticket") ?? "";

  const claims = useMemo(
    () => decodeJwtPayload<BindClaims>(ticket) ?? {},
    [ticket],
  );
  const identity =
    claims.email || claims.display_name || claims.suggested_username || "";

  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!ticket) router.replace("/login?oauth_error=missing_ticket");
  }, [ticket, router]);

  const onCreate = async () => {
    setError("");
    setCreating(true);
    try {
      const res = await oauthApi.register(ticket);
      localStorage.setItem(STORAGE_KEYS.TOKEN, res.token);
      document.cookie = `${STORAGE_KEYS.TOKEN}=${res.token}; path=/; max-age=86400; SameSite=Lax`;
      router.push("/dashboard");
    } catch (err) {
      const code = err instanceof ApiError ? err.message : "register_failed";
      setError(t(`registerError.${code}` as never) || code);
    } finally {
      setCreating(false);
    }
  };

  const onBind = () => {
    router.push(`/oauth/bind?ticket=${encodeURIComponent(ticket)}`);
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-3 text-center">
          <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-muted">
            <KeyRound className="size-6 text-muted-foreground" />
          </div>
          <CardTitle>{t("chooseTitle")}</CardTitle>
          <CardDescription>
            {identity
              ? t("chooseHintIdentified", { identity })
              : t("chooseHintGeneric")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <Button className="w-full" onClick={onCreate} disabled={creating}>
            {creating && <Loader2 className="mr-2 size-4 animate-spin" />}
            {t("chooseCreateButton")}
          </Button>
          <Button
            variant="outline"
            className="w-full"
            onClick={onBind}
            disabled={creating}
          >
            {t("chooseBindButton")}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

export default function ChoosePage() {
  return (
    <Suspense fallback={null}>
      <ChoosePageInner />
    </Suspense>
  );
}
