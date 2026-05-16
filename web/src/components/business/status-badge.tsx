"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";

interface StatusBadgeProps {
  status: number; // 1 = enabled, 0 = disabled
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const t = useTranslations("common");
  return (
    <Badge variant={status === 1 ? "default" : "destructive"}>
      {status === 1 ? t("enabled") : t("disabled")}
    </Badge>
  );
}

interface RoleBadgeProps {
  role: number; // 1 = user, 2 = admin
}

export function RoleBadge({ role }: RoleBadgeProps) {
  const t = useTranslations("users");
  return (
    <Badge variant={role === 2 ? "default" : "secondary"}>
      {role === 2 ? t("roleAdmin") : t("roleUser")}
    </Badge>
  );
}

interface OnlineBadgeProps {
  lastSeen: number; // unix timestamp
  thresholdSeconds?: number;
}

export function OnlineBadge({ lastSeen, thresholdSeconds = 60 }: OnlineBadgeProps) {
  const t = useTranslations("agents");
  const isOnline = lastSeen > 0 && Math.floor(Date.now() / 1000) - lastSeen < thresholdSeconds;
  return (
    <Badge variant={isOnline ? "default" : "secondary"}>
      {isOnline ? t("online") : t("offline")}
    </Badge>
  );
}

interface StreamBadgeProps {
  isStream: boolean;
}

export function StreamBadge({ isStream }: StreamBadgeProps) {
  return (
    <Badge variant={isStream ? "default" : "secondary"}>
      {isStream ? "yes" : "no"}
    </Badge>
  );
}
