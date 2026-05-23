"use client";

import { useTranslations } from "next-intl";
import { Pencil, Link as LinkIcon } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";

import { CopyableText } from "@/components/business/copyable-text";
import { RoleBadge } from "@/components/business/status-badge";
import { useMyIdentities } from "@/lib/api/oauth";
import type { User } from "@/lib/types";
import { formatMoneyCompact } from "@/lib/utils/format";

interface ProfileHeroProps {
  profile: User;
  activeTokenCount: number;
  onEditClick: () => void;
}

function pickInitial(user: User): string {
  return (user.display_name?.trim() || user.username || "U").charAt(0).toUpperCase();
}

export function ProfileHero({ profile, activeTokenCount, onEditClick }: ProfileHeroProps) {
  const t = useTranslations("profile");

  const { data: identities = [] } = useMyIdentities();
  const visibleChips = identities.slice(0, 4);
  const extraCount = Math.max(0, identities.length - visibleChips.length);

  const displayName = profile.display_name?.trim() || profile.username;

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div className="flex flex-col gap-4 md:flex-row md:items-start">
          <Avatar size="lg" className="md:size-[72px]">
            {profile.avatar_url && <AvatarImage src={profile.avatar_url} alt={displayName} />}
            <AvatarFallback className="text-xl">{pickInitial(profile)}</AvatarFallback>
          </Avatar>

          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-lg font-semibold">{displayName}</span>
              <RoleBadge role={profile.role} />
            </div>
            {profile.email && (
              <p className="text-body text-muted-foreground">{profile.email}</p>
            )}
            <div className="flex flex-wrap items-center gap-2 pt-1">
              <CopyableText text={String(profile.id)} display={`#${profile.id}`} />
              {visibleChips.map((i) => (
                <Badge key={i.id} variant="outline" className="gap-1 font-normal">
                  <LinkIcon className="size-3" />
                  {i.provider_display_name}
                </Badge>
              ))}
              {extraCount > 0 && (
                <Badge variant="outline" className="font-normal">+{extraCount}</Badge>
              )}
            </div>
          </div>

          <Button onClick={onEditClick} size="sm" className="self-start md:self-auto">
            <Pencil className="mr-2 size-4" />
            {t("editProfile")}
          </Button>
        </div>

        <Separator />

        <div className="grid grid-cols-3 gap-3">
          <Stat label={t("activeTokens")} value={String(activeTokenCount)} />
          <Stat label={t("remainingQuota")} value={formatMoneyCompact(profile.quota)} />
          <Stat label={t("totalUsed")} value={formatMoneyCompact(profile.used_quota)} />
        </div>
      </CardContent>
    </Card>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <span className="font-semibold tabular-nums">{value}</span>
      <span className="text-label text-muted-foreground uppercase tracking-wide">{label}</span>
    </div>
  );
}
