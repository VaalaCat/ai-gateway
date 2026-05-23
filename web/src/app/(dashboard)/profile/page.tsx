"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";

import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent } from "@/components/ui/card";

import { ProfileFormDialog } from "@/components/business/profile-form-dialog";

import { ProfileHero } from "./profile-hero";
import { ProfileUsageCard } from "./profile-usage-card";
import { ProfileTokensCard } from "./profile-tokens-card";
import { ProfileBYOKCard } from "./profile-byok-card";
import { AccountSecuritySection } from "./account-security-section";

import { useProfile } from "@/lib/api/users";
import { useTokens } from "@/lib/api/tokens";

export default function ProfilePage() {
  const t = useTranslations("profile");

  const { data: profile, isLoading } = useProfile();
  const { data: tokensData } = useTokens({ page_size: 100 });
  const [editOpen, setEditOpen] = useState(false);

  const activeTokenCount = useMemo(() => {
    if (!profile) return 0;
    const list = tokensData?.data ?? [];
    return list.filter((tk) => tk.user_id === profile.id && tk.status === 1).length;
  }, [tokensData, profile]);

  if (isLoading || !profile) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold">{t("title")}</h1>
          <p className="text-muted-foreground mt-1">{t("description")}</p>
        </div>
        <Card>
          <CardContent className="pt-6 space-y-4">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-12 w-full" />
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6">
            <Skeleton className="h-32 w-full" />
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6">
            <Skeleton className="h-32 w-full" />
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("description")}</p>
      </div>

      <ProfileHero
        profile={profile}
        activeTokenCount={activeTokenCount}
        onEditClick={() => setEditOpen(true)}
      />

      <ProfileUsageCard userId={profile.id} />
      <ProfileTokensCard userId={profile.id} />
      <ProfileBYOKCard />
      <AccountSecuritySection />

      <ProfileFormDialog
        mode="self"
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={{
          email: profile.email ?? "",
          display_name: profile.display_name ?? "",
          avatar_url: profile.avatar_url ?? "",
        }}
        fallbackInitial={profile.username}
      />
    </div>
  );
}
