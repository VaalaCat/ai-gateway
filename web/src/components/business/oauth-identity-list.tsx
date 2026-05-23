"use client";

import { Suspense, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { MoreHorizontal } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { OAuthProviderBadge } from "@/components/business/oauth-provider-badge";

import {
  useMyIdentities,
  usePublicOAuthProviders,
  useDeleteIdentity,
  useIssueLinkTicket,
} from "@/lib/api/oauth";
import { ApiError } from "@/lib/api/client";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { OAuthIdentityItem } from "@/lib/types-oauth";

export function OAuthIdentityList() {
  return (
    <Suspense fallback={null}>
      <OAuthIdentityListInner />
    </Suspense>
  );
}

function OAuthIdentityListInner() {
  const t = useTranslations("oauth.profile");
  const tc = useTranslations("common");
  const params = useSearchParams();
  const linkedFlag = params.get("oauth_linked");
  const errorCode = params.get("oauth_error");

  const { data: identities = [] } = useMyIdentities();
  const { data: providers = [] } = usePublicOAuthProviders();
  const del = useDeleteIdentity();
  const issueLink = useIssueLinkTicket();

  const [deleteItem, setDeleteItem] = useState<OAuthIdentityItem | null>(null);

  useEffect(() => {
    if (linkedFlag || errorCode) {
      const url = new URL(window.location.href);
      url.searchParams.delete("oauth_linked");
      url.searchParams.delete("oauth_error");
      window.history.replaceState(null, "", url.toString());
    }
  }, [linkedFlag, errorCode]);

  const linkable = useMemo(
    () => providers.filter((p) => !identities.some((i) => i.provider_name === p.name)),
    [providers, identities],
  );

  const providerByName = useMemo(() => {
    const m = new Map<string, (typeof providers)[number]>();
    for (const p of providers) m.set(p.name, p);
    return m;
  }, [providers]);

  const handleUnlink = async () => {
    if (!deleteItem) return;
    try {
      await del.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
    } catch (err) {
      const code = err instanceof ApiError ? err.message : "unknown";
      toast.error(code === "last_login_method" ? t("lastLoginMethod") : tc("error"));
    } finally {
      setDeleteItem(null);
    }
  };

  const handleLink = async (name: string) => {
    try {
      const { ticket } = await issueLink.mutateAsync();
      window.location.href = `/api/oauth/${name}/link?ticket=${encodeURIComponent(ticket)}`;
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("sectionTitle")}</CardTitle>
        <CardDescription>{t("sectionDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {linkedFlag && (
          <Alert>
            <AlertDescription>{t("linked", { provider: linkedFlag })}</AlertDescription>
          </Alert>
        )}
        {errorCode === "already_linked" && (
          <Alert variant="destructive">
            <AlertDescription>{t("alreadyLinked")}</AlertDescription>
          </Alert>
        )}

        {identities.length === 0 ? (
          <div className="rounded-lg border border-dashed py-8 text-center text-sm text-muted-foreground">
            {t("emptyHint")}
          </div>
        ) : (
          <div className="divide-y rounded-lg border">
            {identities.map((i) => {
              const provider = providerByName.get(i.provider_name);
              return (
                <div key={i.id} className="flex items-center gap-3 px-4 py-3">
                  <OAuthProviderBadge
                    displayName={i.provider_display_name}
                    iconUrl={provider?.icon_url}
                    size="md"
                  />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-sm font-medium">
                      {i.provider_display_name}
                    </span>
                    <span className="truncate text-xs text-muted-foreground">
                      {i.email || i.subject}
                    </span>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon">
                        <MoreHorizontal className="size-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        className="text-destructive"
                        onSelect={() => setDeleteItem(i)}
                      >
                        {t("unlink")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              );
            })}
          </div>
        )}

        {linkable.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                {t("linkButton")}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {linkable.map((p) => (
                <DropdownMenuItem key={p.name} onSelect={() => handleLink(p.name)}>
                  <OAuthProviderBadge
                    displayName={p.display_name}
                    iconUrl={p.icon_url}
                    size="sm"
                    className="mr-2"
                  />
                  {p.display_name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </CardContent>

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(o) => !o && setDeleteItem(null)}
        title={t("unlinkConfirm")}
        description={
          deleteItem
            ? t("unlinkDescription", { provider: deleteItem.provider_display_name })
            : ""
        }
        onConfirm={handleUnlink}
      />
    </Card>
  );
}
