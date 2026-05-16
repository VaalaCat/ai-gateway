"use client";

import { Suspense, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { STORAGE_KEYS } from "@/lib/constants";
import { decodeJwtPayload } from "@/lib/jwt";

interface BindClaims {
  provider_id?: number;
  email?: string;
  display_name?: string;
  suggested_username?: string;
}

function BindPageInner() {
  const t = useTranslations("oauth");
  const tAuth = useTranslations("auth");
  const router = useRouter();
  const params = useSearchParams();
  const ticket = params.get("ticket") ?? "";

  const claims = useMemo(
    () => decodeJwtPayload<BindClaims>(ticket) ?? {},
    [ticket],
  );
  const identity = claims.email || claims.display_name || claims.suggested_username || "";

  const [username, setUsername] = useState(claims.suggested_username ?? "");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!ticket) router.replace("/login?oauth_error=missing_ticket");
  }, [ticket, router]);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const res = await oauthApi.bind(ticket, username, password);
      localStorage.setItem(STORAGE_KEYS.TOKEN, res.token);
      document.cookie = `${STORAGE_KEYS.TOKEN}=${res.token}; path=/; max-age=86400; SameSite=Lax`;
      router.push("/dashboard");
    } catch (err) {
      const code = err instanceof ApiError ? err.message : "ticket_invalid";
      setError(t(`bindError.${code}` as never) || code);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-3 text-center">
          <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-muted">
            <KeyRound className="size-6 text-muted-foreground" />
          </div>
          <CardTitle>{t("bindTitle")}</CardTitle>
          <CardDescription>
            {identity ? t("bindHintIdentified", { identity }) : t("bindHintGeneric")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-4">
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-2">
              <Label htmlFor="username">{tAuth("username")}</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">{tAuth("password")}</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <Button type="submit" className="w-full" disabled={loading}>
              {loading && <Loader2 className="mr-2 size-4 animate-spin" />}
              {t("bindButton")}
            </Button>
          </form>
          <p className="mt-4 text-center text-xs text-muted-foreground">
            {t("bindFooterHint")}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

export default function BindPage() {
  return (
    <Suspense fallback={null}>
      <BindPageInner />
    </Suspense>
  );
}
