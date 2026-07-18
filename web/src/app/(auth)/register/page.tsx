"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { api } from "@/lib/api/client";
import { usePublicConfig } from "@/lib/api/system";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import Link from "next/link";

function RegisterForm() {
  const t = useTranslations("register");
  const router = useRouter();
  const searchParams = useSearchParams();
  const { data: publicConfig } = usePublicConfig();
  const registrationEnabled = publicConfig?.registration_enabled;
  const inviteEnabled = publicConfig?.invite_enabled;
  const [form, setForm] = useState(() => ({
    username: "", email: "", password: "", confirmPassword: "", inviteCode: searchParams.get("invite") ?? "",
  }));
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (form.password !== form.confirmPassword) {
      toast.error(t("passwordMismatch"));
      return;
    }
    setLoading(true);
    try {
      await api.post("/register", {
        username: form.username,
        email: form.email,
        password: form.password,
        invite_code: form.inviteCode,
      });
      toast.success(t("success"));
      router.push("/login");
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : "Registration failed";
      toast.error(message);
    } finally {
      setLoading(false);
    }
  };

  if (registrationEnabled === false) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-md">
          <CardContent className="pt-6 text-center text-muted-foreground">
            Registration is disabled
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <Card className="w-full max-w-md">
        <CardHeader><CardTitle>{t("title")}</CardTitle></CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label>{t("username")}</Label>
              <Input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} required minLength={3} maxLength={32} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="email">{t("email")}</Label>
              <Input id="email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder={t("emailPlaceholder")} required />
            </div>
            <div className="space-y-2">
              <Label>{t("password")}</Label>
              <Input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required minLength={8} />
            </div>
            <div className="space-y-2">
              <Label>{t("confirmPassword")}</Label>
              <Input type="password" value={form.confirmPassword} onChange={(e) => setForm({ ...form, confirmPassword: e.target.value })} required minLength={8} />
            </div>
            {inviteEnabled === true && (
              <div className="space-y-2">
                <Label>{t("inviteCode")}</Label>
                <Input value={form.inviteCode} onChange={(e) => setForm({ ...form, inviteCode: e.target.value })} placeholder={t("inviteCodePlaceholder")} required />
              </div>
            )}
            <Button type="submit" className="w-full" disabled={loading}>{t("submit")}</Button>
          </form>
          <p className="mt-4 text-center text-sm text-muted-foreground">
            <Link href="/login" className="underline">{t("loginLink")}</Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

export default function RegisterPage() {
  return (
    <Suspense>
      <RegisterForm />
    </Suspense>
  );
}
