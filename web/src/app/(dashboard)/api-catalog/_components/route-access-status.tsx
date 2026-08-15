"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import type { InvocationTokenFailure } from "../../api-services/_components/use-invocation-token";
import type { Token } from "@/lib/types";

export interface RouteAccessStatusProps {
  tokenID: number;
  token?: Pick<Token, "id" | "name" | "key">;
  isChecking: boolean;
  failure: InvocationTokenFailure | null;
  effectiveAccess: "granted" | "not_granted" | "unknown";
  effectiveUnavailable?: boolean;
}

export function RouteAccessStatus({
  tokenID,
  token,
  isChecking,
  failure,
  effectiveAccess,
  effectiveUnavailable = false,
}: RouteAccessStatusProps) {
  const t = useTranslations("apiCatalog");
  const canCopyReal = Boolean(token?.key && !isChecking && !failure);
  const message = canCopyReal
    ? t("tokenCallable", { name: token?.name ?? "" })
    : isChecking
      ? t("tokenChecking")
      : failure === "miss"
        ? t("tokenUnavailable")
        : failure === "error"
          ? t("tokenValidationFailed")
          : t("chooseTokenToInvoke");

  return (
    <div
      role="status"
      data-testid="route-access-status"
      data-can-copy-real={String(canCopyReal)}
      data-token-selected={String(tokenID > 0)}
      className="flex min-w-0 flex-wrap items-center gap-2 text-sm text-muted-foreground"
    >
      <Badge variant={canCopyReal ? "secondary" : failure ? "destructive" : "outline"}>
        {message}
      </Badge>
      {failure === "miss" && effectiveAccess === "granted" ? (
        <span>{t("effectiveAccessHint")}</span>
      ) : null}
      {effectiveUnavailable ? (
        <span title={t("accessLoadFailedDescription")}>{t("accessLoadFailed")}</span>
      ) : null}
    </div>
  );
}
