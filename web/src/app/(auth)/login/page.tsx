"use client";

import { useState, useEffect, Suspense } from "react";
import { useRouter } from "next/navigation";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api/client";
import { oauthApi } from "@/lib/api/oauth";
import type { PublicProvider } from "@/lib/types-oauth";
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
import { Separator } from "@/components/ui/separator";
import { OAuthProviderBadge } from "@/components/business/oauth-provider-badge";
import { Loader2 } from "lucide-react";
import Link from "next/link";

function LoginContent() {
  const t = useTranslations("auth");
  const tRegister = useTranslations("register");
  const tOauth = useTranslations("oauth");
  const router = useRouter();
  const { login } = useAuth();
  const searchParams = useSearchParams();
  const oauthError = searchParams.get("oauth_error");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [registrationEnabled, setRegistrationEnabled] = useState(false);
  const [providers, setProviders] = useState<PublicProvider[]>([]);

  useEffect(() => {
    api.get<{ registration_enabled: boolean }>("/system/registration-status")
      .then(res => setRegistrationEnabled(res.registration_enabled))
      .catch(() => {});
  }, []);

  useEffect(() => {
    oauthApi.publicProviders().then(setProviders).catch(() => {});
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      await login(username, password);
      router.push("/dashboard");
    } catch {
      setError(t("loginError"));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <Card className="w-full max-w-md mx-4">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">{t("loginTitle")}</CardTitle>
          <CardDescription>{t("loginSubtitle")}</CardDescription>
        </CardHeader>
        <CardContent>
          {(oauthError || error) && (
            <Alert variant="destructive" className="mb-4">
              <AlertDescription>
                {oauthError ? tOauth(`loginError.${oauthError}` as never) : error}
              </AlertDescription>
            </Alert>
          )}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">{t("usernameOrEmail")}</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder={t("usernameOrEmailPlaceholder")}
                required
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">{t("password")}</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <Button type="submit" className="w-full" disabled={loading}>
              {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              {t("loginButton")}
            </Button>
          </form>
          {providers.length > 0 && (
            <>
              <div className="my-6 flex items-center gap-3">
                <Separator className="flex-1" />
                <span className="text-xs uppercase tracking-wider text-muted-foreground">
                  {tOauth("orContinueWith")}
                </span>
                <Separator className="flex-1" />
              </div>
              <div className="grid gap-2">
                {providers.map(p => (
                  <Button
                    key={p.name}
                    type="button"
                    variant="outline"
                    onClick={() => { window.location.href = `/api/oauth/${p.name}/authorize`; }}
                  >
                    <OAuthProviderBadge
                      displayName={p.display_name}
                      iconUrl={p.icon_url}
                      size="sm"
                      className="mr-2"
                    />
                    {tOauth("loginWith", { provider: p.display_name })}
                  </Button>
                ))}
              </div>
            </>
          )}
          {registrationEnabled && (
            <p className="mt-4 text-center text-sm text-muted-foreground">
              <Link href="/register" className="underline">{tRegister("registerLink")}</Link>
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginContent />
    </Suspense>
  );
}
